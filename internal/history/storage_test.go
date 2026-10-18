package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestFileStorageFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "song")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(root, "objects")
	saved, err := saveFile(ctx, path, objects)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"save missing", func() error { _, err := saveFile(ctx, filepath.Join(root, "missing"), objects); return err }},
		{"storage not directory", func() error { _, err := saveFile(ctx, path, path); return err }},
		{"cancel save", func() error { _, err := saveFile(cancelled, path, objects); return err }},
		{"cancel restore", func() error { return restoreFile(cancelled, path, objects, saved) }},
		{"restore missing object", func() error { return restoreFile(ctx, path, objects, fileState{Hash: "missing"}) }},
		{"restore invalid destination", func() error { return restoreFile(ctx, filepath.Join(path, "nested"), objects, saved) }},
		{"hash directory", func() error { _, err := fileHash(root); return err }},
		{"hash invalid parent", func() error { _, err := fileHash(filepath.Join(path, "nested")); return err }},
		{"remove directory", func() error { return restoreFile(ctx, root, objects, fileState{}) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("accepted invalid storage operation")
			}
		})
	}
	if err = os.WriteFile(filepath.Join(objects, saved.Hash), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = restoreFile(ctx, path, objects, saved); err == nil {
		t.Fatal("restored damaged object")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "audio" {
		t.Fatalf("modified source %q %v", b, err)
	}
}

func TestDatabaseStorageFailures(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	current := filepath.Join(lib, "Database2", "m.db")
	missing := filepath.Join(lib, "missing", "m.db")
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"copy source", func() error { return copyDatabase(ctx, missing, filepath.Join(lib, "copy.db")) }},
		{"copy destination", func() error { return copyDatabase(ctx, current, missing) }},
		{"fingerprint missing", func() error { _, err := fingerprint(ctx, missing); return err }},
		{"fingerprint corrupt", func() error { _, err := fingerprint(ctx, file); return err }},
		{"diff missing", func() error { _, err := changeset(ctx, current, missing); return err }},
		{"diff corrupt after", func() error { _, err := changeset(ctx, current, file); return err }},
		{"diff corrupt before", func() error { _, err := changeset(ctx, file, current); return err }},
		{"restore invalid diff", func() error { return restoreImage(ctx, current, file, current, filepath.Join(lib, "undo.db"), false) }},
		{"identity missing", func() error { _, _, err := trackIdentity(ctx, missing, 1); return err }},
		{"identity corrupt", func() error { _, _, err := trackIdentity(ctx, file, 1); return err }},
		{"identity check missing", func() error { return checkTrackIdentities(ctx, current, missing, current) }},
		{"remove nonempty directory", func() error { return removeDatabase(lib) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("accepted invalid database operation")
			}
		})
	}
}

func TestHistoryStorageFailures(t *testing.T) {
	lib, _, file := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := List(lib); err != nil || len(got) != 0 {
		t.Fatalf("empty history %v %v", got, err)
	}
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"capture destination", func() error { _, err := capture(ctx, lib, file, filepath.Join(dir, "objects"), nil); return err }},
		{"capture invalid library", func() error {
			_, err := capture(ctx, file, filepath.Join(dir, "bad"), filepath.Join(dir, "objects"), nil)
			return err
		}},
		{"lock directory", func() error { _, err := lock(ctx, file); return err }},
		{"entries directory", func() error { _, err := entries(file); return err }},
		{"write directory", func() error { return writeEntry(file, &Entry{}) }},
		{"invalid timestamp", func() error { return writeEntry(dir, &Entry{Created: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("accepted invalid history operation")
			}
		})
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "entry.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = writeEntry(dir, &Entry{ID: "entry"}); err == nil {
		t.Fatal("replaced entry directory")
	}
	if err = os.WriteFile(filepath.Join(dir, "lock.db"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, "", UndoOptions{}); err == nil {
		t.Fatal("accepted corrupt lock")
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	if _, err = List(lib); err == nil {
		t.Fatal("accepted missing storage home")
	}
}

func TestUndoPlanDetectsRacingEdits(t *testing.T) {
	for _, database := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "database"}[database], func(t *testing.T) {
			lib, d, file := fixture(t)
			ctx := context.Background()
			e, err := Run(ctx, lib, "edit", nil, func(context.Context) error {
				if _, err := d.Exec("UPDATE Track SET title='Edited'"); err != nil {
					return err
				}
				return os.WriteFile(file, []byte("changed"), 0600)
			})
			if err != nil {
				t.Fatal(err)
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			p, cleanup, err := prepareUndo(ctx, lib, dir, e, false)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if database {
				_, err = d.Exec("UPDATE Playlist SET title='Raced'")
			} else {
				err = os.WriteFile(file, []byte("raced"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = p.apply(ctx, filepath.Join(dir, "objects")); err == nil {
				t.Fatal("overwrote racing edit")
			}
			var title string
			if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Edited" {
				t.Fatalf("partially applied undo %s %v", title, err)
			}
		})
	}
}

func TestHistoryUUIDAndMovedLibrary(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Information(id INTEGER PRIMARY KEY,uuid TEXT);INSERT INTO Information VALUES(1,'library-uuid')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Changed'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "Library")
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(lib, moved); err != nil {
		t.Fatal(err)
	}
	entries, err := List(moved)
	if err != nil || len(entries) != 1 || entries[0].ID != e.ID {
		t.Fatalf("lost UUID history %v %v", entries, err)
	}
	if _, err = Undo(ctx, moved, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("restored old absolute paths")
	}
}

func TestUndoRefusesChangedSchema(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Changed'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("ALTER TABLE Track ADD COLUMN later TEXT"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("applied incompatible changeset")
	}
}

func TestForceMissingCompositeKey(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Extra(a TEXT,b BLOB,value TEXT,PRIMARY KEY(a,b));INSERT INTO Extra VALUES('key',x'1234','before')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Extra SET value='after'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DELETE FROM Extra"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = d.QueryRow("SELECT value FROM Extra WHERE a='key' AND b=x'1234'").Scan(&value); err != nil || value != "before" {
		t.Fatalf("composite restoration %s %v", value, err)
	}
}

func TestUndoCannotBreakForeignReferences(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Child(id INTEGER PRIMARY KEY,parent INTEGER REFERENCES Playlist(id))"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "create", nil, func(context.Context) error { _, err := d.Exec("INSERT INTO Playlist VALUES(2,'Created')"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Child VALUES(1,2)"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("broke foreign reference")
	}
	c, err := open(ctx, filepath.Join(lib, "Database2", "m.db"), sqlite.OpenReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = sqlitex.ExecuteTransient(c, "PRAGMA foreign_key_check", &sqlitex.ExecOptions{ResultFunc: func(*sqlite.Stmt) error { t.Fatal("source reference broken"); return nil }}); err != nil {
		t.Fatal(err)
	}
}
