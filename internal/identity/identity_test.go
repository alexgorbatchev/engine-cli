package identity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

func fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	lib := t.TempDir()
	d, err := sql.Open("sqlite", filepath.Join(lib, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`CREATE TABLE Information(uuid TEXT); INSERT INTO Information VALUES('native'); CREATE TABLE Track(id INTEGER PRIMARY KEY,originDatabaseUuid TEXT,originTrackId INTEGER); INSERT INTO Track VALUES(1,'origin',42); CREATE TABLE Playlist(id INTEGER PRIMARY KEY,title TEXT);INSERT INTO Playlist VALUES(1,'Set')`); err != nil {
		t.Fatal(err)
	}
	return d, lib
}

func TestRegisterExistingSyncLineage(t *testing.T) {
	d, lib := fixture(t)
	ctx := context.Background()
	state := map[string]any{"Playlists": map[string]string{"1": "playlist:origin:42"}}
	if err := sidecar.WriteJSON(ctx, filepath.Join(lib, ".engine-cli", "sync", "state.json"), state); err != nil {
		t.Fatal(err)
	}
	r, err := Register(ctx, d, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Playlists["1"] != "origin:42" {
		t.Fatalf("lost sync lineage: %v", r.Playlists)
	}
	items, err := List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Resolve(ctx, d, lib, "playlist", items[1].Ref)
	if err != nil || id != 1 {
		t.Fatalf("synced playlist reference: %d %v", id, err)
	}
}

func TestRegistrationAndReferences(t *testing.T) {
	d, lib := fixture(t)
	ctx := context.Background()
	r, err := Register(ctx, d, lib, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Register(ctx, d, lib, false)
	if err != nil || again.ID != r.ID {
		t.Fatalf("registration not idempotent: %v", err)
	}
	refs, err := List(ctx, d, lib)
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs %v %v", refs, err)
	}
	for _, item := range refs {
		id, err := Resolve(ctx, d, lib, item.Kind, item.Ref)
		if err != nil || id != item.ID {
			t.Fatalf("resolve %+v: %d %v", item, id, err)
		}
	}
	ids, err := ResolveBatch(ctx, d, lib, "track", []string{refs[0].Ref, refs[0].Ref})
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 1}) {
		t.Fatalf("batch references: %v %v", ids, err)
	}
	if _, err := d.Exec(`UPDATE Track SET id=99; UPDATE Playlist SET title='Renamed'`); err != nil {
		t.Fatal(err)
	}
	if id, err := Resolve(ctx, d, lib, "track", refs[0].Ref); err != nil || id != 99 {
		t.Fatalf("origin reference changed: %d %v", id, err)
	}
	copyDir := t.TempDir()
	data, err := os.ReadFile(Path(lib))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(copyDir)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(copyDir), data, 0600); err != nil {
		t.Fatal(err)
	}
	copy, err := Register(ctx, d, copyDir, true)
	if err != nil || copy.ID == r.ID {
		t.Fatalf("copy identity: %+v %v", copy, err)
	}
	if _, err := Resolve(ctx, d, copyDir, "track", refs[0].Ref); err == nil {
		t.Fatal("cross-library reference accepted")
	}
	if copy.Playlists["1"] != r.Playlists["1"] {
		t.Fatal("clone lost playlist lineage")
	}
}
