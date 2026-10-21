package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateLibrarySupportsNativeEntities(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	uuid, err := CreateLibrary(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	parent, err := CreatePlaylist(ctx, d, "Sets", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := CreatePlaylist(ctx, d, "Warm up", parent, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO Track(title,path) VALUES('Test','Music/test.flac')"); err != nil {
		t.Fatal(err)
	}
	var origin string
	var track, performance int64
	if err := d.QueryRow("SELECT originDatabaseUuid,originTrackId FROM Track").Scan(&origin, &track); err != nil {
		t.Fatal(err)
	}
	if uuid == "" || origin != uuid || track != 1 {
		t.Fatalf("invalid origin: %s %d, library %s", origin, track, uuid)
	}
	if err := d.QueryRow("SELECT trackId FROM PerformanceData").Scan(&performance); err != nil || performance != track {
		t.Fatalf("performance: %d %v", performance, err)
	}
	if _, err := AddTrackToPlaylist(ctx, d, child, track); err != nil {
		t.Fatal(err)
	}
	if tracks, err := GetPlaylistTracks(ctx, d, child); err != nil || len(tracks) != 1 {
		t.Fatalf("membership: %+v %v", tracks, err)
	}
	if _, err := d.Exec("INSERT INTO Track(path) VALUES('Music/test.flac')"); err == nil {
		t.Fatal("duplicate path accepted")
	}
	if _, err := CreateLibrary(ctx, root); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrote library: %v", err)
	}
	second, err := CreateLibrary(ctx, t.TempDir())
	if err != nil || second == uuid {
		t.Fatalf("library identities collided: %q %v", second, err)
	}
}

func TestCreateLibraryCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateLibrary(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Database2")); !os.IsNotExist(err) {
		t.Fatalf("cancellation created databases: %v", err)
	}
}

func TestCreateLibraryRequiresExplicitDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{"", "   "} {
		if _, err := CreateLibrary(context.Background(), path); err == nil {
			t.Fatalf("accepted empty directory %q", path)
		}
	}
	if _, err := os.Stat("Database2"); !os.IsNotExist(err) {
		t.Fatalf("empty path created a database: %v", err)
	}
}
