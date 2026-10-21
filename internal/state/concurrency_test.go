package state

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestConcurrentStateInitializationAndIndependentPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()}
	var workers sync.WaitGroup
	errors := make(chan error, len(roots))
	for _, root := range roots {
		workers.Add(1)
		go func() {
			defer workers.Done()
			store, err := Open(path)
			if err != nil {
				errors <- err
				return
			}
			defer store.Close()
			errors <- store.Remember(context.Background(), root)
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entries, err := store.List(context.Background())
	if err != nil || len(entries) != len(roots) {
		t.Fatalf("concurrent entries: %+v %v", entries, err)
	}
}

func TestCorruptPreferenceRowsAndUndoStateFailuresAreReported(t *testing.T) {
	ctx := context.Background()
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.database.Exec("DROP TABLE libraries; CREATE TABLE libraries(path,name,hidden,retired); INSERT INTO libraries VALUES('path','name','invalid boolean',0)"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.List(ctx); err == nil {
		t.Fatal("corrupt visibility was silently accepted")
	}
	lib := stateLibrary(t)
	entry, err := history.Run(ctx, lib, "track.update", nil, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err = store.RecordUndo(ctx, &history.Entry{Library: lib, RelocationFrom: t.TempDir(), UndoOf: entry.ID}); err == nil {
		t.Fatal("undo registry failure was ignored")
	}
	if err = os.Remove(filepath.Join(lib, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordUndo(ctx, &history.Entry{Library: lib, RelocationFrom: t.TempDir()}); err == nil {
		t.Fatal("failed registry retirement was ignored")
	}
}
