package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUndoPublicationFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		plan undoPlan
	}{
		{"file became directory", undoPlan{expectedFiles: map[string]fileState{root: {}}}},
		{"database corrupted", undoPlan{expectedDB: map[string]string{file: "old"}}},
		{"database parent replaced", undoPlan{expectedDB: map[string]string{filepath.Join(file, "m.db"): "old"}}},
		{"object missing", undoPlan{files: map[string]fileState{file: {Hash: "missing"}}}},
		{"cannot remove database directory", undoPlan{databases: map[string]string{root: ""}}},
		{"cannot publish database", undoPlan{databases: map[string]string{filepath.Join(root, "new.db"): file}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.plan.apply(ctx, root); err == nil {
				t.Fatal("accepted failed publication")
			}
		})
	}
}

func TestIdentityQueriesRejectInvalidLibraries(t *testing.T) {
	lib, d, file := fixture(t)
	ctx := context.Background()
	missing := filepath.Join(lib, "missing", "m.db")
	for _, p := range []string{missing, file} {
		if err := membershipEntities(ctx, "namespace", p, 1, make(map[string]Entity)); err == nil {
			t.Fatal("accepted invalid membership source")
		}
	}
	path := filepath.Join(lib, "Database2", "m.db")
	if err := membershipEntities(ctx, "namespace", path, 1, make(map[string]Entity)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("DROP TABLE Track;CREATE TABLE Track(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if id, _, err := trackIdentity(ctx, path, 1); err != nil || id != "" {
		t.Fatalf("non-Engine identity %s %v", id, err)
	}
	if _, err := d.Exec("DROP TABLE Track;CREATE TABLE Track(originDatabaseUuid TEXT,originTrackId INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := trackIdentity(ctx, path, 1); err == nil {
		t.Fatal("accepted missing identity column")
	}
}

func TestForceRefusesConstraintConflict(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE UNIQUE INDEX unique_title ON Playlist(title)"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", ScopeCollection, nil, func(context.Context) error { _, err := d.Exec("UPDATE Playlist SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("INSERT INTO Playlist VALUES(2,'Favorites')"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("overwrote unrelated unique row")
	}
}

func TestForceRefusesUnsupportedMissingIdentity(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Extra(id REAL PRIMARY KEY,value TEXT);INSERT INTO Extra VALUES(1.5,'before')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "edit", ScopeCollection, nil, func(context.Context) error { _, err := d.Exec("UPDATE Extra SET value='after'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DELETE FROM Extra"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("accepted unsupported floating-point identity")
	}
}

func TestRestoreImageUnavailableInputs(t *testing.T) {
	lib, d, file := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", ScopeCollection, nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Database2", "m.db")
	before, after := e.Before.Databases[path].Path, e.After.Databases[path].Path
	if err = restoreImage(ctx, path, before, after, filepath.Join(file, "impossible"), false); err == nil {
		t.Fatal("accepted invalid destination")
	}
	if err = checkTrackIdentities(ctx, before, after, file); err == nil {
		t.Fatal("accepted invalid current identity")
	}
	if _, err = d.Exec("CREATE TABLE Unkeyed(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err = restoreImage(ctx, file, before, path, filepath.Join(lib, "restore.db"), true); err == nil {
		t.Fatal("accepted unreadable current database")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = copyDatabase(cancelled, path, filepath.Join(lib, "cancel.db")); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err = copyDatabase(ctx, file, filepath.Join(lib, "corrupt.db")); err == nil {
		t.Fatal("copied invalid database")
	}
}

func TestStoragePermissionFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks require an unprivileged process")
	}
	lib, _, file := fixture(t)
	ctx := context.Background()
	root := t.TempDir()
	objects := filepath.Join(root, "objects")
	state, err := saveFile(ctx, file, objects)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(file, 0600)
	if _, err = saveFile(ctx, file, objects); err == nil {
		t.Fatal("read inaccessible source")
	}
	if _, err = fileHash(file); err == nil {
		t.Fatal("hashed inaccessible source")
	}
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(objects, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(objects, 0700)
	if _, err = saveFile(ctx, file, objects); err == nil {
		t.Fatal("wrote inaccessible object store")
	}
	dest := filepath.Join(root, "destination")
	if err = os.Mkdir(dest, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dest, 0700)
	if err = restoreFile(ctx, filepath.Join(dest, "audio"), objects, state); err == nil {
		t.Fatal("restored inaccessible destination")
	}
	if err = os.Chmod(lib, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(lib, 0700)
	if _, err = capture(ctx, lib, ScopeCollection, filepath.Join(root, "capture"), objects, nil); err == nil {
		t.Fatal("captured inaccessible source")
	}
}
