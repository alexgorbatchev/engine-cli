package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/history"
	"github.com/dj/engine-cli/internal/progress"
	"github.com/dj/engine-cli/internal/transfer"
)

type operation struct {
	Name       string            `json:"name"`
	Mutates    bool              `json:"mutates"`
	Parameters map[string]string `json:"parameters"`
	Required   []string          `json:"required"`
	bind       func(json.RawMessage) (*boundOperation, error)
}

type boundOperation struct {
	run     func(context.Context) (any, error)
	preview bool
	library string
}

type parameterValidator interface{ validate() error }

// define derives accepted JSON fields from the actual request type. The catalog
// and strict decoder therefore describe the same operation contract.
func define[P any](name string, mutates bool, required []string, run func(context.Context, P) (any, error)) operation {
	params := make(map[string]string)
	typ := reflect.TypeFor[P]()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Tag.Get("json") == "-" {
			continue
		}
		params[f.Tag.Get("json")] = f.Type.String()
	}
	return operation{Name: name, Mutates: mutates, Parameters: params, Required: required,
		bind: func(raw json.RawMessage) (*boundOperation, error) {
			var values map[string]json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil || values == nil {
				return nil, fmt.Errorf("request must be a JSON object")
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
				if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) == "" {
					return nil, fmt.Errorf("%s must not be empty", key)
				}
				var list []string
				if json.Unmarshal(value, &list) == nil {
					if len(list) == 0 {
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
			sync, _ := any(p).(transfer.Options)
			return &boundOperation{run: func(ctx context.Context) (any, error) { return run(ctx, p) }, preview: undo.DryRun || sync.DryRun, library: undo.Library}, nil
		}}
}

func (s *Server) database(ctx context.Context, run func(*sql.DB) (any, error)) (any, error) {
	path := filepath.Join(s.config.LibraryDir, "Database2", "m.db")
	// Never let a misspelled library create a new empty database.
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	database, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return run(database)
}

func (s *Server) execute(ctx context.Context, op operation, bound *boundOperation) (result any, historyID string, err error) {
	err = progress.Run(ctx, op.Name, func(ctx context.Context) error {
		lib := s.config.LibraryDir
		if bound.library != "" {
			lib = bound.library
		}
		if op.Mutates && !bound.preview && op.Name != "library.sync" {
			if _, e := backup.AutoBackupIfNeeded(ctx, lib, s.config.NoBackup); e != nil {
				return e
			}
		}
		if op.Mutates && !bound.preview && op.Name != "library.undo" && op.Name != "library.sync" {
			entry, e := history.Run(ctx, s.config.LibraryDir, op.Name, nil, func(ctx context.Context) error { var runErr error; result, runErr = bound.run(ctx); return runErr })
			if entry != nil {
				historyID = entry.ID
			}
			return e
		}
		result, err = bound.run(ctx)
		if r, ok := result.(*transfer.Report); ok && r != nil {
			historyID = r.OperationID
		}
		if entry, ok := result.(*history.Entry); ok && entry != nil && !bound.preview {
			historyID = entry.ID
		}
		return err
	})
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
type identifierParams struct {
	Identifier string `json:"identifier"`
}
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
