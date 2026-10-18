// Package history retains operation snapshots and file contents for selective undo.
package history

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type Entry struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Created   time.Time `json:"created"`
	Library   string    `json:"library"`
	LibraryID string    `json:"library_id"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	UndoOf    string    `json:"undo_of,omitempty"`
	Entities  []Entity  `json:"entities,omitempty"`
	Before    *snapshot `json:"before"`
	After     *snapshot `json:"after,omitempty"`
}

type Entity struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	Key   string `json:"key"`
}

func storeDir(lib string) (string, error) {
	abs, err := filepath.Abs(lib)
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	identity := abs
	c, err := sqlite.OpenConn(filepath.Join(abs, "Database2", "m.db"), sqlite.OpenReadOnly)
	if err == nil {
		// Synthetic libraries may lack Information; production libraries are
		// identified by Engine's UUID so moving a library retains its history.
		ids, e := queryStrings(c, "SELECT uuid FROM Information WHERE uuid IS NOT NULL AND uuid<>''")
		if e == nil && len(ids) == 1 {
			identity = ids[0]
		}
		if err = c.Close(); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(base, "engine-cli", "history", hashText(identity))
	// A restore can replace Information.uuid. Keep the existing history namespace
	// associated with this location so the restore itself remains discoverable.
	alias, err := os.ReadFile(locationPath(abs, dir))
	if err == nil {
		decoded, err := hex.DecodeString(string(alias))
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("library history location record is damaged")
		}
		dir = filepath.Join(filepath.Dir(dir), string(alias))
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if rel, err := filepath.Rel(abs, dir); err != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return "", fmt.Errorf("undo history storage must be outside the source library")
	}
	return dir, nil
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
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, e.ID+".json"), append(data, '\n'))
}

func locationPath(lib, dir string) string {
	return filepath.Join(filepath.Dir(dir), hashText(lib)+".location")
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "entry-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
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
		if e.ID+".json" != file.Name() || e.Library == "" || e.Before == nil || e.Before.Files == nil || e.Before.Databases == nil || (e.After != nil && (e.After.Files == nil || e.After.Databases == nil)) {
			return nil, fmt.Errorf("operation history %s is incomplete or damaged", file.Name())
		}
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

func Run(ctx context.Context, lib, operation string, paths []string, run func(context.Context) error) (*Entry, error) {
	abs, err := filepath.Abs(lib)
	if err != nil {
		return nil, err
	}
	dir, err := storeDir(abs)
	if err != nil {
		return nil, err
	}
	l, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	previous, err := entries(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range previous {
		if e.After == nil {
			return nil, fmt.Errorf("operation %s was interrupted; inspect it and use library undo %s --force to recover", e.ID, e.ID)
		}
	}
	if err = writeAtomic(locationPath(abs, dir), []byte(filepath.Base(dir))); err != nil {
		return nil, fmt.Errorf("preserving library history location: %w", err)
	}
	return runLocked(ctx, abs, dir, operation, "", paths, run)
}

func runLocked(ctx context.Context, lib, dir, operation, undoOf string, paths []string, run func(context.Context) error) (*Entry, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	e := &Entry{ID: hex.EncodeToString(id[:]), Operation: operation, Created: time.Now().UTC(), Library: lib, LibraryID: filepath.Base(dir), State: "pending", UndoOf: undoOf}
	work := filepath.Join(dir, e.ID)
	objects := filepath.Join(dir, "objects")
	before, err := capture(ctx, lib, filepath.Join(work, "before"), objects, paths)
	if err != nil {
		return nil, fmt.Errorf("preserving library before operation: %w", err)
	}
	e.Before = before
	if err = writeEntry(dir, e); err != nil {
		return nil, err
	}
	runErr := run(ctx)
	// Capture actual partial changes after a cancellation or failure too.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	for path := range before.Files {
		paths = append(paths, path)
	}
	e.After, err = capture(finish, lib, filepath.Join(work, "after"), objects, paths)
	if err != nil {
		return e, errors.Join(runErr, fmt.Errorf("operation history is pending; original data is retained: %w", err))
	}
	e.State = "complete"
	if runErr != nil {
		e.State = "failed"
		e.Error = runErr.Error()
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
}

func Undo(ctx context.Context, lib, id string, opts UndoOptions) (*Entry, error) {
	abs, err := filepath.Abs(lib)
	if err != nil {
		return nil, err
	}
	dir, err := storeDir(abs)
	if err != nil {
		return nil, err
	}
	l, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	all, err := entries(dir)
	if err != nil {
		return nil, err
	}
	var target *Entry
	undone := make(map[string]bool)
	for i := range all {
		e := &all[i]
		if e.State == "complete" && e.UndoOf != "" && !undone[e.ID] {
			undone[e.UndoOf] = true
		}
		if target == nil && (e.ID == id || (id == "" && e.UndoOf == "" && !undone[e.ID])) {
			target = e
		}
	}
	if target == nil {
		return nil, fmt.Errorf("operation %q not found in library history", id)
	}
	if undone[target.ID] {
		return nil, fmt.Errorf("operation %s has already been undone", target.ID)
	}
	if target.Library != abs {
		return nil, fmt.Errorf("library has moved from %s; history requires path reconciliation before undo", target.Library)
	}
	recovered := target.After == nil
	if recovered {
		if !opts.Force {
			return nil, fmt.Errorf("operation %s was interrupted; --force recovers changes since its snapshot", target.ID)
		}
		extra := make([]string, 0, len(target.Before.Files))
		for path := range target.Before.Files {
			extra = append(extra, path)
		}
		target.After, err = capture(ctx, abs, filepath.Join(dir, target.ID, "recovered"), filepath.Join(dir, "objects"), extra)
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
	paths := make([]string, 0, len(plan.files))
	for path := range plan.files {
		paths = append(paths, path)
	}
	entry, err := runLocked(ctx, abs, dir, "library.undo", target.ID, paths, func(ctx context.Context) error { return plan.apply(ctx, filepath.Join(dir, "objects")) })
	if err == nil && recovered {
		target.State = "recovered"
		err = writeEntry(dir, target)
	}
	return entry, err
}
