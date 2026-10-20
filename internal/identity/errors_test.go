package identity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityErrors(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name, query string
		register    bool
	}{
		{"wrong library", `UPDATE Information SET uuid='other'`, true},
		{"empty library UUID", `UPDATE Information SET uuid=''`, true},
		{"missing information", `DROP TABLE Information`, true},
		{"missing playlists", `DROP TABLE Playlist`, true},
		{"malformed playlist ID", `DROP TABLE Playlist;CREATE TABLE Playlist(id TEXT);INSERT INTO Playlist VALUES('bad')`, true},
		{"unreadable tracks", `DROP TABLE Track`, false},
		{"unreadable playlists", `DROP TABLE Playlist`, false},
		{"missing origin", `UPDATE Track SET originTrackId=0`, false},
		{"malformed origin", `UPDATE Track SET originTrackId='bad'`, false},
		{"wrong read library", `UPDATE Information SET uuid='other'`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, lib := fixture(t)
			if _, err := Register(ctx, d, lib, false); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(tt.query); err != nil {
				t.Fatal(err)
			}
			if tt.register {
				if _, err := Register(ctx, d, lib, false); err == nil {
					t.Fatal("accepted damaged library")
				}
			} else if _, err := List(ctx, d, lib); err == nil {
				t.Fatal("accepted damaged library")
			}
		})
	}
}

func TestIdentityStorageAndResolution(t *testing.T) {
	ctx := context.Background()
	d, lib := fixture(t)
	if _, err := List(ctx, d, lib); err == nil {
		t.Fatal("accepted unregistered library")
	}
	if _, err := Resolve(ctx, d, lib, "track", "bad"); err == nil {
		t.Fatal("accepted malformed ref")
	}
	if _, err := Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	items, err := List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track VALUES(2,'origin',42)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, d, lib, "track", items[0].Ref); err == nil {
		t.Fatal("accepted duplicate origin")
	}
	if _, err := ResolveDatabase(ctx, d, "playlist", items[1].Ref); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(lib)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LibraryPath(ctx, d); err != nil || got != canonical {
		t.Fatalf("location: %s %v", got, err)
	}
	for _, data := range []string{"broken", `{"version":99}`, `{"version":1,"id":"x","engine_uuid":"native","playlists":null}`} {
		if err := os.WriteFile(Path(lib), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(lib); err == nil {
			t.Fatal("accepted corrupt identity")
		}
		if _, err := Register(ctx, d, lib, false); err == nil {
			t.Fatal("overwrote corrupt identity")
		}
		if _, err := List(ctx, d, lib); err == nil {
			t.Fatal("read corrupt identity")
		}
	}
	if err := os.Remove(Path(lib)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(Path(lib), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(lib); err == nil {
		t.Fatal("accepted directory identity")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LibraryPath(ctx, d); err == nil {
		t.Fatal("read closed database")
	}
	if _, err := ResolveDatabase(ctx, d, "track", items[0].Ref); err == nil {
		t.Fatal("resolved against closed database")
	}
	memory, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	if _, err := LibraryPath(ctx, memory); err == nil {
		t.Fatal("invented persistent memory location")
	}
	path := filepath.Join(t.TempDir(), "Database2", "m.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	disk, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	canonical, err = filepath.EvalSymlinks(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := LibraryPath(ctx, disk); err != nil || got != canonical {
		t.Fatalf("Database2 location: %s %v", got, err)
	}
}
