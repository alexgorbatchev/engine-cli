package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/transfer"
)

type operation struct {
	Name              string            `json:"name"`
	Mutates           bool              `json:"mutates"`
	Effect            string            `json:"effect"`
	ParameterEffects  map[string]string `json:"parameter_effects,omitempty"`
	AdditionalEffects []string          `json:"additional_effects,omitempty"`
	Parameters        map[string]string `json:"parameters"`
	Required          []string          `json:"required"`
	Limits            map[string]int    `json:"limits,omitempty"`
	// HistoryParameters are optional fields every write accepts beside its own
	// parameters; they describe the change on its history entry.
	HistoryParameters []string `json:"history_parameters,omitempty"`
	bind              func(json.RawMessage) (*boundOperation, error)
	reduceResult      func(any, json.RawMessage) any
	collectionRead    bool
}

type boundOperation struct {
	parameters         map[string]json.RawMessage
	before             func(context.Context) error
	run                func(context.Context) (any, error)
	preview            bool
	library            string
	effect             string
	paths              []string
	installsTools      bool
	collectionRead     bool
	replacementLibrary string
	metadata           history.Metadata
}

type parameterValidator interface{ validate() error }

// operationLimiter names the most items each list parameter accepts in one job.
type operationLimiter interface{ operationLimits() map[string]int }

// batchLimitExceeded refuses a list larger than the operation's catalog limit,
// so clients can split the selection instead of showing a generic refusal.
const batchLimitExceeded = "batch_limit_exceeded"

// itemLimits gives every named list parameter the same maximum.
func itemLimits(limit int, parameters ...string) map[string]int {
	limits := make(map[string]int, len(parameters))
	for _, name := range parameters {
		limits[name] = limit
	}
	return limits
}

func checkLimits(values map[string]json.RawMessage, limits map[string]int) error {
	for name, limit := range limits {
		var items []json.RawMessage
		if json.Unmarshal(values[name], &items) == nil && len(items) > limit {
			return &progress.Error{Code: batchLimitExceeded, Cause: fmt.Errorf("%s accepts at most %d items in one job", name, limit)}
		}
	}
	return nil
}

// bindErrorCode keeps request validation under invalid_parameters, except for
// refusals clients act on differently.
func bindErrorCode(err error) string {
	if code := progress.ErrorCode(err); code == batchLimitExceeded {
		return code
	}
	return "invalid_parameters"
}

// historyParameters are the envelope fields a write request may carry for its
// history entry (`history_source`, `group_id`).
var historyParameters = []string{"group_id", "history_source"}

// takeHistoryMetadata removes the history envelope from a write request so the
// operation's own strict decoder sees only its parameters.
func takeHistoryMetadata(raw json.RawMessage, values map[string]json.RawMessage) (json.RawMessage, history.Metadata, error) {
	var meta history.Metadata
	found := false
	for _, key := range historyParameters {
		value, ok := values[key]
		if !ok {
			continue
		}
		found = true
		var text string
		if string(value) != "null" {
			if err := json.Unmarshal(value, &text); err != nil {
				return nil, meta, fmt.Errorf("%s must be a string", key)
			}
		}
		text = strings.TrimSpace(text)
		if len(text) > 200 {
			return nil, meta, fmt.Errorf("%s must be at most 200 characters", key)
		}
		if key == "group_id" {
			meta.GroupID = text
		} else {
			meta.Source = text
		}
		delete(values, key)
	}
	if !found {
		return raw, meta, nil
	}
	stripped, err := json.Marshal(values)
	return stripped, meta, err
}

// define derives accepted JSON fields from the actual request type. The catalog
// and strict decoder therefore describe the same operation contract.
func define[P any](name string, mutates bool, required []string, run func(context.Context, P) (any, error)) operation {
	effect := "read"
	if mutates {
		effect = "collection"
	}
	return defineEffect(name, effect, required, run)
}

func defineEffect[P any](name, effect string, required []string, run func(context.Context, P) (any, error)) operation {
	return defineChecked(name, effect, required, nil, run)
}

// defineChecked adds an optional before check that runs inside the job, ahead of
// snapshots and history, with the decoded parameters and their bound operation.
func defineChecked[P any](name, effect string, required []string, before func(context.Context, P, *boundOperation) error, run func(context.Context, P) (any, error)) operation {
	params := make(map[string]string)
	allowEmpty := make(map[string]bool)
	typ := reflect.TypeFor[P]()
	for _, f := range reflect.VisibleFields(typ) {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || name == "" {
			continue
		}
		params[name] = f.Type.String()
		allowEmpty[name] = f.Tag.Get("allowempty") == "true"
	}
	var parameterEffects map[string]string
	if _, ok := params["auto_install"]; ok {
		parameterEffects = map[string]string{"auto_install": "system"}
	}
	var additionalEffects []string
	if name == "staging.retry" {
		additionalEffects = []string{"system"}
	}
	var limits map[string]int
	if v, ok := any(*new(P)).(operationLimiter); ok {
		limits = v.operationLimits()
	}
	var envelope []string
	// Only effects that record history describe their entry.
	if effect == "collection" || effect == "staging" || effect == "filesystem" {
		envelope = historyParameters
	}
	return operation{Name: name, Mutates: effect != "read", Effect: effect, ParameterEffects: parameterEffects, AdditionalEffects: additionalEffects, Parameters: params, Required: required, Limits: limits, HistoryParameters: envelope,
		bind: func(raw json.RawMessage) (*boundOperation, error) {
			var values map[string]json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil || values == nil {
				return nil, fmt.Errorf("request must be a JSON object")
			}
			if err := checkLimits(values, limits); err != nil {
				return nil, err
			}
			var meta history.Metadata
			if envelope != nil {
				var err error
				if raw, meta, err = takeHistoryMetadata(raw, values); err != nil {
					return nil, err
				}
			}
			var p P
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, err
			}
			if v, ok := any(p).(parameterValidator); ok {
				if err := v.validate(); err != nil {
					return nil, err
				}
			}
			for _, key := range required {
				value, ok := values[key]
				if !ok || string(value) == "null" {
					return nil, fmt.Errorf("%s is required", key)
				}
				var text string
				if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) == "" && !allowEmpty[key] {
					return nil, fmt.Errorf("%s must not be empty", key)
				}
				var list []string
				if json.Unmarshal(value, &list) == nil {
					if len(list) == 0 && !allowEmpty[key] {
						return nil, fmt.Errorf("%s must not be empty", key)
					}
					for _, item := range list {
						if strings.TrimSpace(item) == "" {
							return nil, fmt.Errorf("%s contains an empty item", key)
						}
					}
				}
			}
			undo, _ := any(p).(undoParams)
			library := undo.Library
			if v, ok := any(p).(interface{ operationLibrary() string }); ok {
				library = v.operationLibrary()
			}
			sync, _ := any(p).(transfer.Options)
			boundEffect := effect
			if v, ok := any(p).(interface{ operationEffect() string }); ok {
				boundEffect = v.operationEffect()
			}
			var paths []string
			if v, ok := any(p).(interface{ operationPaths() []string }); ok {
				paths = v.operationPaths()
			}
			preview := undo.DryRun || sync.DryRun
			if v, ok := any(p).(interface{ operationPreview() bool }); ok {
				preview = v.operationPreview()
			}
			// A dry run only reads, whatever its apply would change, so it never
			// waits behind writes or holds the collection from other readers.
			if preview {
				boundEffect = "read"
			}
			bound := &boundOperation{parameters: values, run: func(ctx context.Context) (any, error) { return run(ctx, p) }, preview: preview, library: library, replacementLibrary: sync.Destination, effect: boundEffect, paths: paths, metadata: meta}
			if v, ok := any(p).(interface{ operationCollectionRead() bool }); ok {
				bound.collectionRead = v.operationCollectionRead()
			}
			if v, ok := any(p).(interface{ operationReplacementLibrary() string }); ok {
				bound.replacementLibrary = v.operationReplacementLibrary()
			}
			if raw, ok := values["auto_install"]; ok {
				if err := json.Unmarshal(raw, &bound.installsTools); err != nil {
					return nil, err
				}
			}
			if before != nil {
				bound.before = func(ctx context.Context) error { return before(ctx, p, bound) }
			}
			return bound, nil
		}}
}

func (s *Server) database(ctx context.Context, run func(*sql.DB) (any, error)) (any, error) {
	path := filepath.Join(s.config.LibraryDir, "Database2", "m.db")
	// Never let a misspelled library create a new empty database.
	if _, err := os.Stat(path); err != nil {
		code := "library_unavailable"
		if errors.Is(err, os.ErrNotExist) {
			code = "library_not_found"
		}
		if errors.Is(err, os.ErrPermission) {
			code = "permission_denied"
		}
		return nil, &progress.Error{Code: code, Cause: err}
	}
	database, err := db.Open(path)
	if err != nil {
		return nil, &progress.Error{Code: "library_unavailable", Cause: err}
	}
	defer database.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return run(database)
}

// executeOwned keeps acquisition and cleanup inside the sole terminal progress
// boundary. A committed result and History ID survive a cleanup failure.
func (s *Server) executeOwned(ctx context.Context, op operation, bound *boundOperation, acquire func(context.Context) (func() error, error)) (result any, historyID string, err error) {
	err = progress.Run(ctx, op.Name, func(ctx context.Context) (runErr error) {
		finish, runErr := acquire(ctx)
		if finish != nil {
			defer func() { runErr = errors.Join(runErr, finish()) }()
		}
		if runErr != nil {
			return runErr
		}
		result, historyID, runErr = s.execute(ctx, op, bound)
		return runErr
	})
	return result, historyID, err
}

func (s *Server) execute(ctx context.Context, op operation, bound *boundOperation) (result any, historyID string, err error) {
	effect := bound.effect
	if effect == "" {
		effect = op.Effect
	}
	err = func() error {
		if bound.before != nil {
			if err := bound.before(ctx); err != nil {
				return err
			}
		}
		if effect == "filesystem" {
			var e error
			effect, e = s.mediaPathsEffect(ctx, bound.paths)
			if e != nil {
				return e
			}
		}
		ctx = context.WithValue(ctx, mediaEffectKey{}, effect)
		ctx = history.WithMetadata(ctx, bound.metadata)
		if effect == "collection" && !bound.preview {
			r, e := s.checkSystem(ctx, nil)
			if e != nil {
				return e
			}
			if e := systemcheck.AllowMutation(r); e != nil {
				return e
			}
		}
		lib := s.config.LibraryDir
		if bound.library != "" {
			lib = bound.library
		}
		if op.Name == "library.create" {
			if e := os.MkdirAll(lib, 0700); e != nil {
				return e
			}
		}
		// Creation has no pre-existing database to back up. Its before/after
		// history still records the new databases so creation can be undone.
		prepare := func(ctx context.Context) error {
			if effect != "collection" || op.Name == "library.sync" || op.Name == "library.create" || op.Name == "library.undo" {
				return nil
			}
			_, e := backup.AutoBackupIfNeeded(ctx, lib, s.config.NoBackup)
			return e
		}
		if (effect == "collection" || effect == "staging" || effect == "filesystem") && !bound.preview && op.Name != "library.undo" && op.Name != "library.sync" && !ownsSmartlistRepairPublication(op.Name) {
			// Staging and server-file effects never write Engine databases.
			scope := history.ScopeCollection
			if effect == "staging" || effect == "filesystem" {
				scope = history.ScopeFiles
				if e := os.MkdirAll(lib, 0700); e != nil {
					return e
				}
			}
			entry, e := history.RunPrepared(ctx, lib, history.RunOptions{
				Operation:        op.Name,
				Scope:            scope,
				Prepare:          prepare,
				RepairSmartlists: effect == "collection" && op.Name != "library.create" && op.Name != "library.restore",
			}, func(ctx context.Context) error { var runErr error; result, runErr = bound.run(ctx); return runErr })
			if entry != nil {
				historyID = entry.ID
			}
			return e
		}
		// Sync owns recovery checks for its explicit source and destination.
		// Its unbound fallback library is unrelated to either endpoint.
		if effect == "collection" && !bound.preview && op.Name != "library.sync" && !ownsSmartlistRepairPublication(op.Name) {
			if e := history.CheckSmartlistRepair(ctx, lib); e != nil {
				return e
			}
			if e := prepare(ctx); e != nil {
				return e
			}
		}
		if op.collectionRead || bound.collectionRead {
			result, err = history.ReadSmartlistSafe(ctx, lib, bound.run)
		} else {
			result, err = bound.run(ctx)
		}
		if r, ok := result.(*transfer.Report); ok && r != nil {
			historyID = r.OperationID
		}
		if entry, ok := result.(*history.Entry); ok && entry != nil && !bound.preview {
			historyID = entry.ID
		}
		if repair, ok := result.(*history.SmartlistRepairResult); ok && repair != nil {
			historyID = repair.UndoID
		}
		return err
	}()
	return result, historyID, err
}

func decodeRequest(r io.Reader) (json.RawMessage, error) {
	var raw json.RawMessage
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("provide exactly one JSON object")
	}
	return raw, nil
}

type emptyParams struct{}
type directoryParams struct {
	OutDir string `json:"out_dir"`
}
type pathParams struct {
	Path string `json:"path"`
}

func batch[T any](ctx context.Context, phase string, items []string, run func(context.Context, string) (T, error)) ([]T, error) {
	results := make([]T, 0, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		itemCtx := progress.Range(ctx, 10+85*float64(i)/float64(len(items)), 10+85*float64(i+1)/float64(len(items)))
		result, err := run(itemCtx, item)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		progress.Item(ctx, phase, i+1, len(items))
	}
	return results, nil
}
