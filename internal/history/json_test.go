package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONIsUndoable(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	path := filepath.Join(lib, ".engine-cli", "sync", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "library.sync", ScopeFiles, nil, func(ctx context.Context) error {
		return WriteJSON(ctx, path, map[string]int{"version": 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"version":2}` {
		t.Fatalf("written %q err=%v", data, err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"version":1}` {
		t.Fatalf("restored %q err=%v", data, err)
	}
}

func TestWriteJSONStopsWhenCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WriteJSON(ctx, path, map[string]int{"version": 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("wrote after cancellation: %v", err)
	}
}

func TestWriteJSONReportsUnpreservableFile(t *testing.T) {
	lib, _, _ := fixture(t)
	path := filepath.Join(lib, "state.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), lib, "library.sync", ScopeFiles, nil, func(ctx context.Context) error {
		return WriteJSON(ctx, path, map[string]int{"version": 2})
	})
	if err == nil {
		t.Fatal("replaced a directory without preserving it")
	}
}
