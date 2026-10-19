package db

import (
	"context"
	"github.com/dj/engine-cli/internal/identity"
	"testing"
)

func TestSelectorsNeverResolveLabels(t *testing.T) {
	d, _ := createTestDB(t)
	if _, err := d.Exec(`INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Set',0),(2,'Set',3),(3,'Folder',0); INSERT INTO Track(id,title,filename,path) VALUES(1,'Song','same.mp3','a/same.mp3'),(2,'Song','same.mp3','b/same.mp3')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, label := range []string{"Set", "Folder", "-1"} {
		if p, err := FindPlaylist(ctx, d, label); err == nil || p != nil {
			t.Errorf("playlist label %q resolved: %v %v", label, p, err)
		}
	}
	for _, label := range []string{"Song", "same.mp3", "a/", "%", "-1"} {
		if tr, err := FindTrack(ctx, d, label); tr != nil {
			t.Errorf("track label %q resolved: %v %v", label, tr, err)
		}
	}
	if p, err := FindPlaylist(ctx, d, "2"); err != nil || p == nil || p.ID != 2 {
		t.Fatalf("ID: %v %v", p, err)
	}
	if tr, err := FindTrack(ctx, d, "b/same.mp3"); err != nil || tr == nil || tr.ID != 2 {
		t.Fatalf("exact path: %v %v", tr, err)
	}
}

func TestStableSelectors(t *testing.T) {
	d, _ := createTestDB(t)
	if _, err := d.Exec(`INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Set',0);INSERT INTO Track(id,title,path) VALUES(1,'Song','song.mp3')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := FindTrack(ctx, d, "eng:missing:track:missing"); err == nil {
		t.Fatal("accepted unregistered track reference")
	}
	if _, err := FindPlaylist(ctx, d, "eng:missing:playlist:missing"); err == nil {
		t.Fatal("accepted unregistered playlist reference")
	}
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	items, err := identity.List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == "track" {
			tr, err := FindTrack(ctx, d, item.Ref)
			if err != nil || tr == nil || tr.ID != item.ID {
				t.Fatalf("track ref: %v %v", tr, err)
			}
		} else {
			pl, err := FindPlaylist(ctx, d, item.Ref)
			if err != nil || pl == nil || pl.ID != item.ID {
				t.Fatalf("playlist ref: %v %v", pl, err)
			}
		}
	}
}
