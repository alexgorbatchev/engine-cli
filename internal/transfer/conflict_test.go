package transfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncConflictReverseAndStalePreview(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Original','song.mp3','song.mp3'); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Parent',0,0); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Child',1,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(2,1,'source',0)`)
	if err := os.WriteFile(filepath.Join(source, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o.DryRun = true
	r, err := Run(context.Background(), o)
	if err != nil || !r.Applicable || len(r.Changes) != 0 {
		t.Fatalf("unchanged: %+v %v", r, err)
	}
	execSQL(t, d, "UPDATE Track SET title='USB edit'")
	r, err = Run(context.Background(), o)
	if err != nil || !r.Applicable || r.Summary.TracksUpdated != 0 {
		t.Fatalf("destination edit: %+v %v", r, err)
	}
	reverse := Options{Source: dest, Destination: source, Scope: Scope{Root: true}, NoBackup: true}
	if _, err = Run(context.Background(), reverse); err != nil {
		t.Fatal(err)
	}
	var title string
	if err = s.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "USB edit" {
		t.Fatalf("reverse title %s %v", title, err)
	}
	execSQL(t, s, "UPDATE Track SET title='Local edit'")
	execSQL(t, d, "UPDATE Track SET title='Another USB edit'")
	r, err = Run(context.Background(), o)
	if err != nil || r.Applicable || len(r.Conflicts) == 0 {
		t.Fatalf("conflict: %+v %v", r, err)
	}
	// Clients explain conflicts by code; the reason stays readable for the CLI.
	if r.Conflicts[0].Code != "sync_diverged" || r.Conflicts[0].Reason == "" {
		t.Fatalf("conflict code: %+v", r.Conflicts[0])
	}
	o.Conflict = "source"
	r, err = Run(context.Background(), o)
	if err != nil || !r.Applicable {
		t.Fatalf("resolution: %+v %v", r, err)
	}
	o.PlanToken = r.PlanToken
	o.DryRun = false
	execSQL(t, d, "UPDATE Track SET comment='Changed after preview'")
	if _, err = Run(context.Background(), o); err == nil {
		t.Fatal("accepted stale preview")
	}
}

func TestSyncMirrorRetainsSharedTracks(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Set',0,0)`)
	o := Options{Source: source, Destination: dest, Scope: Scope{PlaylistIDs: []int64{1}}, NoBackup: true}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Shared','shared.mp3','shared.mp3'); INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Other',0,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'dest',0),(2,1,'dest',0)`)
	if err := os.WriteFile(filepath.Join(dest, "shared.mp3"), []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Mirror = true
	o.Conflict = "source"
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared track: %d %v", count, err)
	}
	if err := d.QueryRow("SELECT count(*) FROM PlaylistEntity WHERE listId=1").Scan(&count); err != nil || count != 0 {
		t.Fatalf("mirrored membership: %d %v", count, err)
	}
}
