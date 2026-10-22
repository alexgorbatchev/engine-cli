package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestStateDefaultLocationsAndInvalidOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, value := range []string{"", "relative", t.TempDir()} {
		t.Setenv("XDG_STATE_HOME", value)
		path, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		root := value
		if !filepath.IsAbs(root) {
			root = filepath.Join(home, ".local", "state")
		}
		if path != filepath.Join(root, "engine-cli", "state.db") {
			t.Fatalf("wrong state path: %s", path)
		}
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_STATE_HOME", "")
	if _, err := DefaultPath(); err == nil {
		t.Fatal("missing home accepted")
	}
}

func TestStateRefusesForeignAndDamagedDatabases(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"foreign", "damaged", "future", "directory", "blocked-parent"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			switch kind {
			case "foreign":
				d, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = d.Exec("CREATE TABLE Track(id INTEGER);INSERT INTO Track VALUES(42)"); err != nil {
					t.Fatal(err)
				}
				d.Close()
			case "damaged":
				if err := os.WriteFile(path, []byte("unrelated file"), 0600); err != nil {
					t.Fatal(err)
				}
			case "future":
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Remember(ctx, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				if _, err = s.database.Exec("PRAGMA user_version=99"); err != nil {
					t.Fatal(err)
				}
				s.Close()
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "blocked-parent":
				if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "child.db")
			}
			if s, err := Open(path); err == nil {
				s.Close()
				t.Fatal("unsafe state database accepted")
			}
			if kind == "foreign" {
				d, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				var id, count int
				if err = d.QueryRow("SELECT id FROM Track").Scan(&id); err != nil || id != 42 {
					t.Fatalf("foreign data changed: %d %v", id, err)
				}
				if err = d.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='libraries'").Scan(&count); err != nil || count != 0 {
					t.Fatalf("state schema leaked into foreign DB: %v", err)
				}
			}
		})
	}
}

func TestStateValidatesNamesAndRetainsHiddenTombstones(t *testing.T) {
	ctx := context.Background()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := t.TempDir()
	if err = s.Remember(ctx, "relative"); err == nil {
		t.Fatal("relative registry key accepted")
	}
	for _, name := range []string{" ", strings.Repeat("x", 201)} {
		if err = s.Update(ctx, path, &name, nil); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	name, hidden := "Cosmetic name", true
	if err = s.Update(ctx, path, &name, &hidden); err != nil {
		t.Fatal(err)
	}
	if err = s.Retire(ctx, path); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(ctx)
	if err != nil || !entries[0].Retired {
		t.Fatalf("retirement lost: %+v %v", entries, err)
	}
	if err = s.Remember(ctx, path); err != nil {
		t.Fatal(err)
	}
	entries, err = s.List(ctx)
	if err != nil || entries[0].Retired || !entries[0].Hidden || entries[0].Name != name {
		t.Fatalf("rediscovery changed preferences: %+v %v", entries, err)
	}
	s.Close()
	if _, err = s.List(ctx); err == nil {
		t.Fatal("read from closed state DB succeeded")
	}
	if err = s.Update(ctx, path, nil, &hidden); err == nil {
		t.Fatal("write to closed state DB succeeded")
	}
	if err = s.Retire(ctx, path); err == nil {
		t.Fatal("retirement on closed state DB succeeded")
	}
}

func stateLibrary(t *testing.T) string {
	t.Helper()
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = db.InitSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestUndoReconcilesMovedLibraryLocations(t *testing.T) {
	ctx := context.Background()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := stateLibrary(t)
	destination := filepath.Join(t.TempDir(), "moved")
	if err = s.Remember(ctx, source); err != nil {
		t.Fatal(err)
	}
	e, err := history.Run(ctx, source, "library.move", history.ScopeCollection, nil, func(ctx context.Context) error { return history.RelocateLibrary(ctx, source, destination) })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Retire(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err = s.Remember(ctx, destination); err != nil {
		t.Fatal(err)
	}
	undone, err := history.Undo(ctx, destination, e.ID, history.UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUndo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range entries {
		if lib.Path == source && (lib.Hidden || lib.Retired) {
			t.Fatalf("source not restored: %+v", lib)
		}
		if lib.Path == destination && (!lib.Hidden || !lib.Retired) {
			t.Fatalf("moved location remains active: %+v", lib)
		}
	}
	if err = s.RecordUndo(ctx, &history.Entry{Library: source, UndoOf: "ordinary-edit"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUndo(ctx, &history.Entry{Library: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing undo location accepted")
	}
}

func TestUndoCreationRetiresTheRemovedLibrary(t *testing.T) {
	ctx := context.Background()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lib := t.TempDir()
	entry, err := history.Run(ctx, lib, "library.create", history.ScopeCollection, nil, func(ctx context.Context) error {
		if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
			return err
		}
		d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
		if err != nil {
			return err
		}
		defer d.Close()
		return db.InitSchema(ctx, d)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Remember(ctx, lib); err != nil {
		t.Fatal(err)
	}
	undone, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUndo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(ctx)
	if err != nil || !entries[0].Retired {
		t.Fatalf("removed library still active: %+v %v", entries, err)
	}
}
