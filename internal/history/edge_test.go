package history

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
)

func TestCaptureRejectsUnpreservableFiles(t *testing.T) {
	lib, d, file := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(lib, "link")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err = Run(ctx, lib, "edit", []string{link}, func(context.Context) error { called = true; return nil }); err == nil || called {
		t.Fatal("modified library before preserving files")
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "interrupted", []string{link}, func(context.Context) error { return os.Symlink(file, link) })
	if err == nil || e == nil {
		t.Fatalf("missing pending record %v %v", e, err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("recovered unknown file kind")
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	if _, err = capture(ctx, lib, filepath.Join(dir, "invalid"), filepath.Join(dir, "objects"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureExcludesOwnStorage(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	s, err := capture(ctx, lib, filepath.Join(dir, "test"), filepath.Join(dir, "objects"), []string{""})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 0 {
		t.Fatalf("captured own storage: %v", s.Files)
	}
	empty := t.TempDir()
	if _, err = capture(ctx, empty, filepath.Join(dir, "empty"), filepath.Join(dir, "objects"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestUndoDatabasePresenceConflicts(t *testing.T) {
	for _, created := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "created"}[created], func(t *testing.T) {
			lib, _, _ := fixture(t)
			ctx := context.Background()
			source := filepath.Join(lib, "Database2", "m.db")
			extra := filepath.Join(lib, "extra.db")
			if !created {
				if err := copyDatabase(ctx, source, extra); err != nil {
					t.Fatal(err)
				}
			}
			e, err := Run(ctx, lib, "database", nil, func(context.Context) error {
				if created {
					return copyDatabase(ctx, source, extra)
				}
				return os.Remove(extra)
			})
			if err != nil {
				t.Fatal(err)
			}
			if created {
				if err = os.Remove(extra); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = copyDatabase(ctx, source, extra); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err == nil {
				t.Fatal("accepted replacement conflict")
			}
			if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUndoRestoresTriggerEffectsOnce(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec(`CREATE TABLE Audit(id INTEGER PRIMARY KEY,value TEXT);
 CREATE TRIGGER audit_edit AFTER UPDATE ON Track BEGIN INSERT INTO Audit(value) VALUES(NEW.title);END`); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = d.QueryRow("SELECT count(*) FROM Audit").Scan(&n); err != nil || n != 0 {
		t.Fatalf("replayed trigger %d %v", n, err)
	}
	if _, err = d.Exec("UPDATE Track SET title='Later'"); err != nil {
		t.Fatal(err)
	}
	if err = d.QueryRow("SELECT count(*) FROM Audit").Scan(&n); err != nil || n != 1 {
		t.Fatalf("lost trigger %d %v", n, err)
	}
}

func TestForceAlreadyRemovedInsertion(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "add", nil, func(context.Context) error {
		_, err := d.Exec("INSERT INTO Track VALUES(2,'New','new.mp3','origin',8)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DELETE FROM Track WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err == nil {
		t.Fatal("accepted missing inserted row")
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryReadAndLockErrors(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "lock.db"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = lock(ctx, dir); err == nil {
		t.Fatal("accepted directory lock")
	}
	if err = os.Remove(filepath.Join(dir, "lock.db")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bad.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(ctx, lib, "edit", nil, func(context.Context) error { return nil }); err == nil {
		t.Fatal("accepted bad history")
	}
	if _, err = Undo(ctx, lib, "", UndoOptions{}); err == nil {
		t.Fatal("accepted bad undo history")
	}
	if err = os.Remove(filepath.Join(dir, "bad.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "bad.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = List(lib); err == nil {
		t.Fatal("accepted unreadable history entry")
	}
	if err = os.Remove(filepath.Join(dir, "bad.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", lib)
	t.Setenv("HOME", "")
	if entries, err := List(lib); err != nil || len(entries) != 0 {
		t.Fatalf("portable storage %v %v", entries, err)
	}
}

func TestInvalidChangesetReader(t *testing.T) {
	it, err := sqlite.NewChangesetIterator(bytes.NewReader([]byte("invalid changeset")))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	if err = collectEntities(context.Background(), "lib", "before", "after", it, make(map[string]Entity)); err == nil {
		t.Fatal("accepted damaged changeset")
	}
}

func TestUndoPreflightDamagedFiles(t *testing.T) {
	for _, damage := range []string{"saved file", "current symlink", "current database", "history directory"} {
		t.Run(damage, func(t *testing.T) {
			lib, d, file := fixture(t)
			ctx := context.Background()
			e, err := Run(ctx, lib, "edit", []string{file}, func(context.Context) error {
				if _, err := d.Exec("UPDATE Track SET title='Edited'"); err != nil {
					return err
				}
				return os.WriteFile(file, []byte("edited"), 0600)
			})
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "saved file":
				err = os.Remove(filepath.Join(dir, "objects", e.Before.Files[file].Hash))
			case "current symlink":
				if err = os.Remove(file); err == nil {
					err = os.Symlink("missing", file)
				}
			case "current database":
				if err = d.Close(); err == nil {
					err = os.WriteFile(filepath.Join(lib, "Database2", "m.db"), []byte("bad"), 0600)
				}
			case "history directory":
				dir = file
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, cleanup, err := prepareUndo(ctx, lib, dir, e, true); err == nil {
				cleanup()
				t.Fatal("accepted damaged input")
			}
		})
	}
}
