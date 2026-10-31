// Package history retains operation snapshots and file contents for selective undo.
package history

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type Entry struct {
	RelocationFrom string `json:"relocation_from,omitempty"`
	cleanup        func() error
	ID             string     `json:"id"`
	Operation      string     `json:"operation"`
	Scope          Scope      `json:"scope"`
	Created        time.Time  `json:"created"`
	Library        string     `json:"library"`
	LibraryID      string     `json:"library_id"`
	State          string     `json:"state"`
	Error          string     `json:"error,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	UndoOf         string     `json:"undo_of,omitempty"`
	Entities       []Entity   `json:"entities,omitempty"`
	Moves          []fileMove `json:"moves,omitempty"`
	Before         *snapshot  `json:"before"`
	After          *snapshot  `json:"after,omitempty"`
}

// Entry IDs are random, hex encoded, and name the entry and its work directory.
const entryIDBytes = 16

// entryTempPrefix names entries being written atomically.
const entryTempPrefix = "entry-"

type Entity struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	Key   string `json:"key"`
}

func storeDir(lib string) (string, error) {
	if lib == "" {
		return "", fmt.Errorf("empty engine library directory")
	}
	abs, err := filepath.Abs(lib)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("library is not a directory: %s", abs)
	}
	return filepath.Join(abs, ".engine-cli", "history"), nil
}

func lock(ctx context.Context, dir string) (*sqlite.Conn, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c, err := open(ctx, filepath.Join(dir, "lock.db"), sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, err
	}
	c.SetBusyTimeout(0)
	if err = sqlitex.ExecuteTransient(c, "BEGIN IMMEDIATE", nil); err != nil {
		c.Close()
		return nil, fmt.Errorf("another engine-cli operation is using this library: %w", err)
	}
	return c, nil
}

func writeEntry(dir string, e *Entry) error {
	data, err := json.MarshalIndent(portableEntry(*e), "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, e.ID+".json"), append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, entryTempPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return publishEntry(f.Name(), path)
}

func entries(dir string) ([]Entry, error) {
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Entry, 0)
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var e Entry
		if err = json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("reading operation history: %w", err)
		}
		// Entries recorded before scopes existed snapshotted every database.
		if e.Scope == "" {
			e.Scope = ScopeCollection
		}
		if e.ID+".json" != file.Name() || e.Library == "" || e.Scope.validate() != nil || e.Before == nil || e.Before.Files == nil || e.Before.Databases == nil || (e.After != nil && (e.After.Files == nil || e.After.Databases == nil)) {
			return nil, fmt.Errorf("operation history %s is incomplete or damaged", file.Name())
		}
		relocateEntry(&e, filepath.Dir(filepath.Dir(dir)))
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	return result, nil
}

func List(lib string) ([]Entry, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	return entries(dir)
}

func Run(ctx context.Context, lib, operation string, scope Scope, paths []string, run func(context.Context) error) (result *Entry, err error) {
	return RunPrepared(ctx, lib, RunOptions{Operation: operation, Scope: scope, Paths: paths}, run)
}

// RunPrepared holds the History owner across preparation and the operation's
// snapshots. Callers establish fresh Desktop absence before entering this owner.
func RunPrepared(ctx context.Context, lib string, options RunOptions, run func(context.Context) error) (result *Entry, err error) {
	if err = options.Scope.validate(); err != nil {
		return nil, err
	}
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	abs := filepath.Dir(filepath.Dir(dir))
	l, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, l.Close())
		if result != nil && result.cleanup != nil {
			err = errors.Join(err, result.cleanup())
		}
	}()
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range previous {
		if e.After == nil {
			return nil, &progress.Error{Code: "history_recovery_required", Cause: fmt.Errorf("operation %s was interrupted; inspect it and use library undo %s --force to recover", e.ID, e.ID)}
		}
	}
	if err = collect(dir, previous); err != nil {
		return nil, fmt.Errorf("removing unreferenced operation history: %w", err)
	}
	if options.Prepare != nil {
		if err := options.Prepare(ctx); err != nil {
			return nil, err
		}
	}
	return runLocked(ctx, abs, dir, options.Operation, "", options.Scope, options.Paths, run)
}

func runLocked(ctx context.Context, lib, dir, operation, undoOf string, scope Scope, paths []string, run func(context.Context) error) (*Entry, error) {
	var id [entryIDBytes]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	e := &Entry{ID: hex.EncodeToString(id[:]), Operation: operation, Created: time.Now().UTC(), Library: lib, LibraryID: hex.EncodeToString(id[:]), State: "pending", UndoOf: undoOf, Scope: scope}
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	if len(previous) > 0 {
		e.LibraryID = previous[0].LibraryID
	}
	work := filepath.Join(dir, e.ID)
	objects := filepath.Join(dir, "objects")
	before, err := capture(ctx, lib, scope, filepath.Join(work, "before"), objects, paths)
	if err != nil {
		return nil, fmt.Errorf("preserving library before operation: %w", err)
	}
	e.Before = before
	if err = writeEntry(dir, e); err != nil {
		return nil, err
	}
	r := &recorder{entry: e, dir: dir}
	runErr := run(context.WithValue(ctx, recorderKey{}, r))
	r.mu.Lock()
	recoveryRequired := r.recoveryRequired
	r.mu.Unlock()
	if recoveryRequired {
		return e, &progress.Error{Code: "history_recovery_required", Cause: errors.Join(runErr, fmt.Errorf("operation %s has an incomplete cross-file publication; inspect it and use library undo %s --force to recover", e.ID, e.ID))}
	}
	lib, dir = e.Library, r.dir
	work = filepath.Join(dir, e.ID)
	// Capture actual partial changes after a cancellation or failure too.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if scope.capturesDatabases() {
		// RelocateLibrary rebases snapshot paths in place when the library moves.
		engineBefore := before.Databases[filepath.Join(lib, "Database2", "m.db")]
		if engineBefore.Path != "" {
			if err := tags.ReconcileRemoved(finish, lib, engineBefore.Path); err != nil {
				return e, &progress.Error{Code: "history_recovery_required", Cause: errors.Join(runErr, fmt.Errorf("reconciling custom tags; operation %s remains pending for recovery: %w", e.ID, err))}
			}
			if err := colors.ReconcileRemoved(finish, lib, engineBefore.Path); err != nil {
				return e, &progress.Error{Code: "history_recovery_required", Cause: errors.Join(runErr, fmt.Errorf("reconciling track colors; operation %s remains pending for recovery: %w", e.ID, err))}
			}
		}
	}
	e.After, err = captureAfter(finish, lib, scope, filepath.Join(work, "after"), before)
	if err != nil {
		return e, errors.Join(runErr, fmt.Errorf("operation history is pending; original data is retained: %w", err))
	}
	r.mu.Lock()
	workspaces := r.workspaces
	r.mu.Unlock()
	// A failure that only touched its own retryable workspace is not a library change to undo.
	if runErr != nil && onlyWorkspaceChanges(e, workspaces) {
		if err = discard(dir, e); err != nil {
			return nil, errors.Join(runErr, fmt.Errorf("discarding history of a failed operation: %w", err))
		}
		return nil, runErr
	}
	e.State = "complete"
	if runErr != nil {
		e.State = "failed"
		e.Error = runErr.Error()
		e.ErrorCode = progress.ErrorCode(runErr)
	}
	e.Entities, err = changedEntities(finish, e)
	if err != nil {
		return e, errors.Join(runErr, err)
	}
	return e, errors.Join(runErr, writeEntry(dir, e))
}

type UndoOptions struct {
	Force  bool
	DryRun bool
	// Prepare runs under the History owner after validation, before Undo's snapshot.
	Prepare func(context.Context) error
}

func Undo(ctx context.Context, lib, id string, opts UndoOptions) (result *Entry, err error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	abs := filepath.Dir(filepath.Dir(dir))
	var ownership *undoOwnership
	if !opts.DryRun {
		ownership, err = ownUndo(ctx, abs, dir, id)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, ownership.lease.Close()) }()
	}
	l, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, l.Close())
		if result != nil && result.cleanup != nil {
			err = errors.Join(err, result.cleanup())
		}
	}()
	all, err := entries(dir)
	if err != nil {
		return nil, err
	}
	target, err := selectUndoTarget(all, id)
	if err != nil {
		return nil, err
	}
	if ownership != nil {
		if err := ownership.validate(target); err != nil {
			return nil, err
		}
	}
	if target.RelocationFrom != "" {
		if _, e := os.Lstat(target.RelocationFrom); !os.IsNotExist(e) {
			return nil, fmt.Errorf("undo conflict: previous library location is occupied")
		}
	}
	recovered := target.After == nil
	if recovered {
		if !opts.Force {
			return nil, fmt.Errorf("operation %s was interrupted; --force recovers changes since its snapshot", target.ID)
		}
		target.After, err = captureAfter(ctx, abs, target.Scope, filepath.Join(dir, target.ID, "recovered"), target.Before)
		if err != nil {
			return nil, err
		}
	}
	plan, cleanup, err := prepareUndo(ctx, abs, dir, target, opts.Force)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if opts.DryRun {
		return target, nil
	}
	if opts.Prepare != nil {
		if err := opts.Prepare(ctx); err != nil {
			return nil, err
		}
	}
	entry, err := runLocked(ctx, abs, dir, "library.undo", target.ID, target.Scope, nil, func(ctx context.Context) error {
		if err := plan.apply(ctx, filepath.Join(dir, "objects")); err != nil {
			return err
		}
		if target.RelocationFrom != "" {
			return RelocateLibrary(ctx, abs, target.RelocationFrom)
		}
		return nil
	})
	if err == nil && recovered {
		target.State = "recovered"
		err = writeEntry(dir, target)
	}
	return entry, err
}
