package history

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeDecidesWhetherDatabasesAreSnapshotted(t *testing.T) {
	tests := []struct {
		scope         Scope
		wantDatabases bool
	}{
		{scope: ScopeCollection, wantDatabases: true},
		{scope: ScopeFiles},
	}
	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			lib, _, song := fixture(t)
			ctx := context.Background()
			e, err := Run(ctx, lib, "staging.rm", tt.scope, nil, func(ctx context.Context) error {
				if err := Preserve(ctx, song); err != nil {
					return err
				}
				return os.Remove(song)
			})
			if err != nil {
				t.Fatal(err)
			}
			if e.Scope != tt.scope {
				t.Fatalf("scope=%q", e.Scope)
			}
			dir := filepath.Join(lib, ".engine-cli", "history")
			if got := databaseCopies(t, filepath.Join(dir, e.ID)); (got > 0) != tt.wantDatabases || (len(e.Before.Databases) > 0) != tt.wantDatabases || (len(e.After.Databases) > 0) != tt.wantDatabases {
				t.Fatalf("copies=%d before=%v after=%v", got, e.Before.Databases, e.After.Databases)
			}
			undo, err := Undo(ctx, lib, e.ID, UndoOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(song); err != nil || string(data) != "original audio" {
				t.Fatalf("restored %q err=%v", data, err)
			}
			if undo.Scope != tt.scope || (databaseCopies(t, filepath.Join(dir, undo.ID)) > 0) != tt.wantDatabases {
				t.Fatalf("undo scope=%q databases=%v", undo.Scope, undo.Before.Databases)
			}
		})
	}
}

func TestStoredScopes(t *testing.T) {
	tests := []struct {
		name    string
		stored  any
		want    Scope
		damaged bool
	}{
		{name: "files", stored: "files", want: ScopeFiles},
		{name: "recorded before scopes", want: ScopeCollection},
		{name: "unknown", stored: "everything", damaged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib, _, _ := fixture(t)
			e, err := Run(context.Background(), lib, "track.update", ScopeFiles, nil, func(context.Context) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lib, ".engine-cli", "history", e.ID+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var stored map[string]any
			if err = json.Unmarshal(data, &stored); err != nil {
				t.Fatal(err)
			}
			delete(stored, "scope")
			if tt.stored != nil {
				stored["scope"] = tt.stored
			}
			if data, err = json.Marshal(stored); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			list, err := List(lib)
			if tt.damaged {
				if err == nil || !strings.Contains(err.Error(), "damaged") {
					t.Fatalf("accepted %v: %v", tt.stored, err)
				}
				return
			}
			if err != nil || len(list) != 1 || list[0].Scope != tt.want {
				t.Fatalf("history=%+v err=%v", list, err)
			}
		})
	}
}

func TestRunRejectsUnknownScope(t *testing.T) {
	lib, _, _ := fixture(t)
	ran := false
	if _, err := Run(context.Background(), lib, "track.update", Scope("everything"), nil, func(context.Context) error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("created history storage: %v", err)
	}
}

func databaseCopies(t *testing.T, work string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return filepath.SkipAll
		}
		if err == nil && !d.IsDir() && filepath.Ext(path) == ".db" {
			count++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}
