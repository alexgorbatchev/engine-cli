package transfer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestScopeDoesNotIncludeAncestorTracks(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Parent track','parent.mp3','parent.mp3'),('Child track','child.mp3','child.mp3'); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Parent',0,0); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Child',1,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0),(2,2,'source',0)`)
	put(t, filepath.Join(source, "child.mp3"), "child")
	o := Options{Source: source, Destination: dest, Scope: Scope{PlaylistIDs: []int64{2}}, NoBackup: true}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 1 {
		t.Fatalf("ancestor media included: %d %v", count, err)
	}
	o.DryRun = true
	r, err := Run(context.Background(), o)
	if err != nil || !r.Applicable || len(r.Changes) != 0 {
		t.Fatalf("repeat %+v %v", r, err)
	}
}

func TestAudioReplacementUndoAndMediaNone(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Track','song.mp3','song.mp3')")
	put(t, filepath.Join(source, "song.mp3"), "old audio")
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := d.QueryRow("SELECT path FROM Track").Scan(&path); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(source, "song.mp3"), "new longer audio")
	o.Media = "none"
	o.DryRun = true
	r, err := Run(context.Background(), o)
	if err != nil || r.Applicable {
		t.Fatalf("media none %+v %v", r, err)
	}
	o.Media = "changed"
	o.DryRun = false
	r, err = Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, path))
	if err != nil || string(b) != "new longer audio" {
		t.Fatalf("replacement %q %v", b, err)
	}
	if _, err = history.Undo(context.Background(), dest, r.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(dest, path))
	if err != nil || string(b) != "old audio" {
		t.Fatalf("restoration %q %v", b, err)
	}
}

func TestMappingAndCollision(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	for _, database := range []*sql.DB{s, d} {
		execSQL(t, database, "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Same',0,0),('Other',0,1)")
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{PlaylistIDs: []int64{1}}, DryRun: true}
	r, err := Run(context.Background(), o)
	if err != nil || r.Applicable {
		t.Fatalf("unmapped collision %+v %v", r, err)
	}
	o.PlaylistMap = []PlaylistMapping{{1, 1}}
	o.Conflict = "source"
	o.DryRun = false
	o.NoBackup = true
	if _, err = Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, mappings := range [][]PlaylistMapping{{{1, 1}, {1, 2}}, {{1, 1}, {2, 1}}, {{1, 2}}, {{99, 1}}} {
		o.PlaylistMap = mappings
		o.DryRun = true
		if _, err = Run(context.Background(), o); err == nil {
			t.Fatalf("accepted ambiguous mapping %v", mappings)
		}
	}
}

func TestBadLibraryData(t *testing.T) {
	for _, tt := range []struct {
		name string
		sql  string
	}{
		{"identity", "UPDATE Information SET uuid=''"},
		{"schema", "ALTER TABLE Track ADD COLUMN unknown TEXT"},
		{"missing table", "DROP TABLE Smartlist"},
		{"missing origin", "DROP TRIGGER trigger_after_insert_Track_fix_origin; INSERT INTO Track(title,path) VALUES('Bad','a.mp3')"},
		{"missing parent", "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Orphan',999,0)"},
		{"cyclic parent", "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Cycle',1,0)"},
		{"missing member", "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,99,'absent',0)"},
		{"membership cycle", "INSERT INTO Track(title,path) VALUES('A','a'); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0); INSERT INTO PlaylistEntity(id,listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,1,'source',1)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source, s := library(t, "source")
			dest, _ := library(t, "dest")
			execSQL(t, s, tt.sql)
			if r, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true}); err == nil && r.Applicable {
				t.Fatal("accepted invalid library")
			}
		})
	}
}

func TestDamagedSyncState(t *testing.T) {
	for _, content := range []string{"not json", `{"Version":1,"Library":"wrong"}`, `{"Version":999}`} {
		t.Run(content, func(t *testing.T) {
			source, _ := library(t, "source")
			dest, _ := library(t, "dest")
			put(t, statePath(dest), content)
			if _, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true}); err == nil {
				t.Fatal("accepted damaged state")
			}
		})
	}
}

func TestRefuseSymlinksAndUnownedMedia(t *testing.T) {
	for _, kind := range []string{"symlink", "existing", "directory"} {
		t.Run(kind, func(t *testing.T) {
			source, s := library(t, "source")
			dest, _ := library(t, "dest")
			execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Track','song.mp3','song.mp3')")
			put(t, filepath.Join(source, "song.mp3"), "audio")
			target := filepath.Join(dest, "Music", "engine-cli", digest("track:source:1"), "song.mp3")
			if kind == "symlink" {
				if err := os.Symlink(t.TempDir(), filepath.Join(dest, "Music")); err != nil {
					t.Fatal(err)
				}
			} else if kind == "existing" {
				put(t, target, "unowned")
			} else {
				if err := os.Mkdir(filepath.Join(dest, "Music"), 0700); err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(dest, "Music", "engine-cli"), "not a directory")
			}
			r, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true})
			if err == nil && r.Applicable {
				t.Fatal("unsafe media plan accepted")
			}
		})
	}
}

func TestFailedDatabaseWriteLeavesUndoableFiles(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Track','song.mp3','song.mp3')")
	put(t, filepath.Join(source, "song.mp3"), "audio")
	execSQL(t, d, "CREATE TRIGGER fail_sync BEFORE INSERT ON Track BEGIN SELECT RAISE(ABORT,'write refused'); END")
	r, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true})
	if err == nil || r.OperationID == "" {
		t.Fatalf("failed operation %+v %v", r, err)
	}
	if _, err = history.Undo(context.Background(), dest, r.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMirrorReplacesUnmappedPlaylistWithSameTitle(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	for _, database := range []*sql.DB{s, d} {
		execSQL(t, database, "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Same',0,0)")
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, Mirror: true, NoBackup: true}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM Playlist").Scan(&count); err != nil || count != 1 {
		t.Fatalf("playlists %d %v", count, err)
	}
}
