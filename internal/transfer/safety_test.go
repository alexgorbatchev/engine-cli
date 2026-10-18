package transfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/backup"
	"github.com/dj/engine-cli/internal/history"
)

func TestMirrorRemovalAndUndo(t *testing.T) {
	source, _ := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Remove','remove.mp3','remove.mp3'); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Parent',0,0); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Child',1,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(2,1,'dest',0); INSERT INTO Smartlist(listUuid,title) VALUES('smart','Smart')`)
	file := filepath.Join(dest, "remove.mp3")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, Mirror: true, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.TracksRemoved != 1 || r.Summary.PlaylistsRemoved != 2 {
		t.Fatalf("summary %+v", r.Summary)
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("media remains: %v", err)
	}
	if _, err = history.Undo(context.Background(), dest, r.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != "original" {
		t.Fatalf("undo media %q %v", b, err)
	}
}

func TestSyncArtworkAndSmartlist(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	hash := strings.Repeat("ab", 20)
	execSQL(t, d, "INSERT INTO AlbumArt(hash,albumArt) VALUES('unrelated',x'01')")
	execSQL(t, s, `INSERT INTO AlbumArt(hash,albumArt) VALUES(?,x'010203'); INSERT INTO Track(title,path,filename,albumArtId,albumArt,albumArtSourceHash) VALUES('Art','song.mp3','song.mp3',1,'image://planck/1',?); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('smart','Smart','','','','{}')`, hash, hash)
	execSQL(t, s, "INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,0)")
	if err := os.WriteFile(filepath.Join(source, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "Artwork"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Artwork", hash+".jpg"), []byte("artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
	r, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.FilesCopied != 2 {
		t.Fatalf("copies %+v", r.Summary)
	}
	var prepared int
	if err = d.QueryRow("SELECT count(*) FROM PreparelistEntity JOIN Track ON Track.id=PreparelistEntity.trackId WHERE originDatabaseUuid='source'").Scan(&prepared); err != nil || prepared != 1 {
		t.Fatalf("prepare list %d %v", prepared, err)
	}
	var art string
	if err = d.QueryRow("SELECT albumArt FROM Track").Scan(&art); err != nil || art != "image://planck/2" {
		t.Fatalf("art reference %q %v", art, err)
	}
	execSQL(t, s, "UPDATE Smartlist SET title='Updated' WHERE listUuid='smart'")
	if _, err = Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err = d.QueryRow("SELECT title FROM Smartlist").Scan(&art); err != nil || art != "Updated" {
		t.Fatalf("smartlist %q %v", art, err)
	}
}

func TestSyncInvalidRequests(t *testing.T) {
	source, s := library(t, "source")
	dest, _ := library(t, "dest")
	for _, tt := range []struct {
		name   string
		change func(*Options)
	}{
		{"no source", func(o *Options) { o.Source = "" }}, {"missing destination", func(o *Options) { o.Destination = filepath.Join(dest, "missing") }},
		{"same library", func(o *Options) { o.Destination = source }}, {"nested", func(o *Options) { o.Destination = filepath.Join(source, "Database2") }},
		{"both scopes", func(o *Options) { o.Scope.PlaylistIDs = []int64{1} }}, {"no scope", func(o *Options) { o.Scope.Root = false }},
		{"negative ID", func(o *Options) { o.Scope = Scope{PlaylistIDs: []int64{-1}} }}, {"missing ID", func(o *Options) { o.Scope = Scope{PlaylistIDs: []int64{999}} }},
		{"invalid media", func(o *Options) { o.Media = "all" }}, {"invalid conflict", func(o *Options) { o.Conflict = "latest" }},
		{"missing mapping", func(o *Options) { o.PlaylistMap = []PlaylistMapping{{SourceID: 1, DestinationID: 1}} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true}
			tt.change(&o)
			if _, err := Run(context.Background(), o); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Options{Source: source, Destination: dest, Scope: Scope{Root: true}}); err == nil {
		t.Fatal("ignored cancellation")
	}
	execSQL(t, s, "UPDATE Information SET schemaVersionMajor=99")
	if _, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}}); err == nil {
		t.Fatal("accepted unknown schema")
	}
}

func TestSyncMissingMediaAndConflictPolicies(t *testing.T) {
	for _, policy := range []string{"error", "source", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			dest, d := library(t, "dest")
			execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Source','song.mp3','song.mp3')")
			execSQL(t, d, "INSERT INTO Track(title,path,filename,originDatabaseUuid,originTrackId) VALUES('Destination','song.mp3','song.mp3','source',1)")
			for _, path := range []string{source, dest} {
				if err := os.WriteFile(filepath.Join(path, "song.mp3"), []byte("audio"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, Conflict: policy, NoBackup: true}
			r, err := Run(context.Background(), o)
			if policy == "error" {
				if err == nil || r.Applicable {
					t.Fatal("unresolved conflict applied")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var title string
			if err = d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil {
				t.Fatal(err)
			}
			want := "Source"
			if policy == "destination" {
				want = "Destination"
			}
			if title != want {
				t.Fatalf("title %s want %s", title, want)
			}
			if err = os.Remove(filepath.Join(source, "song.mp3")); err != nil {
				t.Fatal(err)
			}
			o.DryRun = true
			o.Conflict = "source"
			r, err = Run(context.Background(), o)
			if err != nil || r.Applicable {
				t.Fatalf("missing media: %+v %v", r, err)
			}
		})
	}
}

func TestSyncBackupAndPrepareRemoval(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Track','song.mp3','song.mp3');INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,0)")
	put(t, filepath.Join(source, "song.mp3"), "audio")
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	backups, err := backup.ListBackups(dest, "")
	if err != nil || len(backups) != 1 {
		t.Fatalf("destination snapshots %v %v", backups, err)
	}
	execSQL(t, s, "DELETE FROM PreparelistEntity")
	o.Mirror = true
	if _, err = Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = d.QueryRow("SELECT count(*) FROM PreparelistEntity").Scan(&count); err != nil || count != 0 {
		t.Fatalf("prepare list %d %v", count, err)
	}
	if err = os.RemoveAll(filepath.Join(dest, ".engine-cli", "backups")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dest, ".engine-cli", "backups"), "blocked")
	execSQL(t, s, "UPDATE Track SET title='Update'")
	if _, err = Run(context.Background(), o); err == nil {
		t.Fatal("ignored backup failure")
	}
}

func TestMirrorRefusesExternalAudioDeletion(t *testing.T) {
	source, _ := library(t, "source")
	dest, d := library(t, "dest")
	file := filepath.Join(t.TempDir(), "external.mp3")
	put(t, file, "keep external audio")
	execSQL(t, d, "INSERT INTO Track(title,path,filename) VALUES('External',?,'external.mp3')", file)
	r, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, Mirror: true, DryRun: true})
	if err != nil || r.Applicable {
		t.Fatalf("external removal %+v %v", r, err)
	}
}

func TestPreparelistAdditiveSyncRetainsDestinationEntries(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Source','song.mp3','song.mp3');INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,0)")
	execSQL(t, d, "INSERT INTO Track(title,path,filename) VALUES('Destination','other.mp3','other.mp3');INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,0)")
	put(t, filepath.Join(source, "song.mp3"), "source audio")
	put(t, filepath.Join(dest, "other.mp3"), "destination audio")
	opts := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, Conflict: "source", NoBackup: true}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM PreparelistEntity").Scan(&count); err != nil || count != 2 {
		t.Fatalf("prepare entries %d %v", count, err)
	}
	if r, err := Run(context.Background(), opts); err != nil || r.OperationID != "" {
		t.Fatalf("unchanged additive sync %+v %v", r, err)
	}
	execSQL(t, s, "INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(999,1)")
	if _, err := Run(context.Background(), Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true}); err == nil {
		t.Fatal("accepted missing prepared track")
	}
}
