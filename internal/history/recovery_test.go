package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryStableIdentityAcrossRelocation(t *testing.T) {
	lib, d, p := fixture(t)
	ctx := context.Background()
	r, err := Run(ctx, lib, "track.title", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(lib, "moved.mp3")
	moved, err := Run(ctx, lib, "track.move", nil, func(ctx context.Context) error {
		if err := MoveFile(ctx, p, dest); err != nil {
			return err
		}
		_, err := d.Exec("UPDATE Track SET path='moved.mp3'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entities) != 1 || len(moved.Entities) != 1 || r.Entities[0].ID != moved.Entities[0].ID {
		t.Fatalf("identity changed: %v %v", r.Entities, moved.Entities)
	}
	if _, err = Undo(ctx, lib, r.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var title, path string
	if err = d.QueryRow("SELECT title,path FROM Track").Scan(&title, &path); err != nil || title != "Original" || path != "moved.mp3" {
		t.Fatalf("%s %s %v", title, path, err)
	}
	if _, err = os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryTracksPlaylistMembershipByEntity(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE PlaylistEntity(id INTEGER PRIMARY KEY,listId INTEGER,trackId INTEGER,databaseUuid TEXT);INSERT INTO PlaylistEntity VALUES(1,1,7,'origin');INSERT INTO Playlist VALUES(2,'Other')"); err != nil {
		t.Fatal(err)
	}
	edit, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	move, err := Run(ctx, lib, "playlist.track.move", nil, func(context.Context) error { _, err := d.Exec("UPDATE PlaylistEntity SET listId=2"); return err })
	if err != nil {
		t.Fatal(err)
	}
	found := false
	playlists := 0
	for _, e := range move.Entities {
		if e.ID == edit.Entities[0].ID {
			found = true
		}
		if e.Table == "Playlist" {
			playlists++
		}
	}
	if !found || playlists != 2 {
		t.Fatalf("missing related identities: %+v", move.Entities)
	}
	if _, err = Undo(ctx, lib, edit.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var list int
	if err = d.QueryRow("SELECT listId FROM PlaylistEntity").Scan(&list); err != nil || list != 2 {
		t.Fatalf("lost later membership: %d %v", list, err)
	}
}

func TestHistoryDistinguishesBinaryEntityKeys(t *testing.T) {
	lib, d, _ := fixture(t)
	if _, err := d.Exec("CREATE TABLE BinaryEntity(id BLOB PRIMARY KEY,value TEXT);INSERT INTO BinaryEntity VALUES(x'ff','before'),(x'fe','before'),(CAST(x'ff' AS TEXT),'before'),(CAST(x'fe' AS TEXT),'before')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(context.Background(), lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE BinaryEntity SET value='after'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Entities) != 4 || e.Entities[0].ID == e.Entities[1].ID {
		t.Fatalf("binary identity collision: %+v", e.Entities)
	}
}

func TestHistoryDistinguishesRealEntityKeys(t *testing.T) {
	lib, d, _ := fixture(t)
	if _, err := d.Exec("CREATE TABLE RealEntity(id REAL PRIMARY KEY,value TEXT);INSERT INTO RealEntity VALUES(1.0,'before'),(1.0000000000000002,'before')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(context.Background(), lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE RealEntity SET value='after'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Entities) != 2 {
		t.Fatalf("real identity collision: %+v", e.Entities)
	}
}

func TestUndoFailedOperationAndRedo(t *testing.T) {
	lib, d, p := fixture(t)
	ctx := context.Background()
	failure := errors.New("failed after modifying track")
	r, err := Run(ctx, lib, "partial", []string{p}, func(context.Context) error {
		if _, err := d.Exec("UPDATE Track SET title='Partial'"); err != nil {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || r.State != "failed" || r.ErrorCode != "operation_failed" {
		t.Fatalf("entry=%+v err=%v", r, err)
	}
	u, err := Undo(ctx, lib, r.ID, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, r.ID, UndoOptions{}); err == nil {
		t.Fatal("undid twice")
	}
	if _, err = Undo(ctx, lib, u.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Partial" {
		t.Fatalf("redo title=%s err=%v", title, err)
	}
	if _, err = os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("redo did not remove file: %v", err)
	}
}

func TestUndoLatestWalksBackHistory(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	for _, title := range []string{"First", "Second"} {
		if _, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title=?", title); return err }); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"First", "Original"} {
		if _, err := Undo(ctx, lib, "", UndoOptions{}); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := d.QueryRow("SELECT title FROM Track").Scan(&got); err != nil || got != want {
			t.Fatalf("got %s want %s: %v", got, want, err)
		}
	}
}

func TestUndoLibraryUUIDReplacement(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	if _, err := d.Exec("CREATE TABLE Information(id INTEGER PRIMARY KEY,uuid TEXT);INSERT INTO Information VALUES(1,'original-library')"); err != nil {
		t.Fatal(err)
	}
	e, err := Run(ctx, lib, "library.restore", nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Information SET uuid='replacement-library';UPDATE Track SET title='Replacement',originDatabaseUuid='replacement-origin',originTrackId=88")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	var uuid string
	if err = d.QueryRow("SELECT uuid FROM Information").Scan(&uuid); err != nil || uuid != "original-library" {
		t.Fatalf("UUID=%s %v", uuid, err)
	}
}

func TestForceMissingTrackAndForeignIdentity(t *testing.T) {
	for _, identity := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "different identity"}[identity], func(t *testing.T) {
			lib, d, _ := fixture(t)
			ctx := context.Background()
			r, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Exec("DELETE FROM Track"); err != nil {
				t.Fatal(err)
			}
			if identity {
				if _, err = d.Exec("INSERT INTO Track VALUES(1,'Other','other.mp3','foreign',99)"); err != nil {
					t.Fatal(err)
				}
			}
			_, err = Undo(ctx, lib, r.ID, UndoOptions{Force: true})
			if identity {
				if err == nil || !strings.Contains(err.Error(), "identity") {
					t.Fatalf("identity protection: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var title string
			if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
				t.Fatalf("restored=%s err=%v", title, err)
			}
		})
	}
}

func TestUndoRefusesLostTrackIdentity(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	e, err := Run(ctx, lib, "edit", nil, func(context.Context) error { _, err := d.Exec("UPDATE Track SET title='Edited'"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("UPDATE Track SET originDatabaseUuid=''"); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, e.ID, UndoOptions{Force: true}); err == nil {
		t.Fatal("modified track with lost identity")
	}
}

func TestUndoDatabaseCreationAndDeletion(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "remove"}[remove], func(t *testing.T) {
			lib, _, _ := fixture(t)
			ctx := context.Background()
			path := filepath.Join(lib, "extra.db")
			create := func() error { return copyDatabase(ctx, filepath.Join(lib, "Database2", "m.db"), path) }
			if remove {
				if err := create(); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Run(ctx, lib, "database", nil, func(context.Context) error {
				if remove {
					return os.Remove(path)
				}
				return create()
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Undo(ctx, lib, r.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(path)
			if remove && err != nil {
				t.Fatal(err)
			}
			if !remove && !os.IsNotExist(err) {
				t.Fatalf("new database retained: %v", err)
			}
		})
	}
}

func TestUndoSchemaChangesAndUnkeyedTables(t *testing.T) {
	for _, schemaChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "unkeyed", true: "schema"}[schemaChange], func(t *testing.T) {
			lib, d, _ := fixture(t)
			ctx := context.Background()
			if _, err := d.Exec("CREATE TABLE Unkeyed(value TEXT);INSERT INTO Unkeyed VALUES('before')"); err != nil {
				t.Fatal(err)
			}
			r, err := Run(ctx, lib, "restore", nil, func(context.Context) error {
				q := "UPDATE Unkeyed SET value='after'"
				if schemaChange {
					q = "CREATE TABLE Custom(id INTEGER PRIMARY KEY)"
				}
				_, err := d.Exec(q)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Exec("UPDATE Playlist SET title='Later'"); err != nil {
				t.Fatal(err)
			}
			if _, err = Undo(ctx, lib, r.ID, UndoOptions{}); err == nil {
				t.Fatal("accepted whole database conflict")
			}
			if _, err = Undo(ctx, lib, r.ID, UndoOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			var value string
			if err = d.QueryRow("SELECT value FROM Unkeyed").Scan(&value); err != nil || value != "before" {
				t.Fatalf("unkeyed restoration %s %v", value, err)
			}
			var count int
			if err = d.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='Custom'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("schema restoration %d %v", count, err)
			}
		})
	}
}

func TestHistoryLockCancellationAndStorageErrors(t *testing.T) {
	lib, _, p := fixture(t)
	ctx := context.Background()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	l, err := lock(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err = Run(ctx, lib, "locked", nil, func(context.Context) error { called = true; return nil }); err == nil || called {
		t.Fatal("concurrent operation ran")
	}
	l.Close()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = Run(cancelled, lib, "cancelled", nil, func(context.Context) error { called = true; return nil }); err == nil || called {
		t.Fatal("cancelled operation ran")
	}
	if _, err = Undo(ctx, lib, "missing", UndoOptions{}); err == nil {
		t.Fatal("missing history accepted")
	}
	if err = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = List(lib); err == nil {
		t.Fatal("broken history accepted")
	}
	os.Remove(filepath.Join(dir, "broken.json"))
	if _, err = saveFile(ctx, lib, filepath.Join(dir, "objects")); err == nil {
		t.Fatal("saved directory")
	}
	if err = restoreFile(ctx, p, dir, fileState{Hash: "../unsafe"}); err == nil {
		t.Fatal("accepted invalid hash")
	}
	t.Setenv("XDG_DATA_HOME", lib)
	if _, err = List(lib); err != nil {
		t.Fatal(err)
	}
}
