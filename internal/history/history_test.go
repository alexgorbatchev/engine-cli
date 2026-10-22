package history

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func fixture(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "Database2"), 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err = d.Exec(`CREATE TABLE Track(id INTEGER PRIMARY KEY,title TEXT,path TEXT,originDatabaseUuid TEXT,originTrackId INTEGER); INSERT INTO Track VALUES(1,'Original','song.mp3','origin',7); CREATE TABLE Playlist(id INTEGER PRIMARY KEY,title TEXT); INSERT INTO Playlist VALUES(1,'Favorites')`); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(lib, "song.mp3")
	if err := os.WriteFile(p, []byte("original audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	return lib, d, p
}

func TestUndoPreservesLaterChangesAndForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated", true: "conflict"}[force], func(t *testing.T) {
			lib, d, p := fixture(t)
			ctx := context.Background()
			record, err := Run(ctx, lib, "track.edit", ScopeCollection, []string{p}, func(context.Context) error {
				if _, err := d.Exec("UPDATE Track SET title='Changed' WHERE id=1"); err != nil {
					return err
				}
				return os.WriteFile(p, []byte("changed audio"), 0o600)
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Exec("UPDATE Playlist SET title='Later'"); err != nil {
				t.Fatal(err)
			}
			if force {
				if _, err = d.Exec("UPDATE Track SET title='Conflict'"); err != nil {
					t.Fatal(err)
				}
				if _, err = Undo(ctx, lib, record.ID, UndoOptions{}); err == nil {
					t.Fatal("accepted conflict")
				}
			}
			if _, err = Undo(ctx, lib, record.ID, UndoOptions{Force: force}); err != nil {
				t.Fatal(err)
			}
			var title string
			if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Original" {
				t.Fatalf("track=%s err=%v", title, err)
			}
			if err = d.QueryRow("SELECT title FROM Playlist").Scan(&title); err != nil || title != "Later" {
				t.Fatalf("playlist=%s err=%v", title, err)
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != "original audio" {
				t.Fatalf("audio=%q err=%v", b, err)
			}
			entries, err := List(lib)
			if err != nil || len(entries) != 2 {
				t.Fatalf("history=%v err=%v", entries, err)
			}
		})
	}
}

func TestUndoFileConflictAndPreview(t *testing.T) {
	lib, _, p := fixture(t)
	ctx := context.Background()
	r, err := Run(ctx, lib, "track.rm", ScopeCollection, []string{p}, func(context.Context) error { return os.Remove(p) })
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("external replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, r.ID, UndoOptions{}); err == nil {
		t.Fatal("accepted file conflict")
	}
	if _, err = Undo(ctx, lib, r.ID, UndoOptions{Force: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "external replacement" {
		t.Fatal("preview changed file")
	}
	if _, err = Undo(ctx, lib, r.ID, UndoOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "original audio" {
		t.Fatal("did not restore file")
	}
}
