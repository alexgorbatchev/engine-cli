package playlist

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestGetPlaylistHierarchy(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// Insert Root Folder (ID 1), Child Playlist (ID 2), Sub-Child (ID 3), Root Playlist (ID 4)
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (1, 'EDM', 0);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (2, 'House', 1);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (3, 'Deep House', 2);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (4, 'Inbox', 0);")

	// Insert Tracks and link to Deep House
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, title, artist, length) VALUES (10, 'Track 10', 'Artist 10', 300);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, title, artist, length) VALUES (20, 'Track 20', 'Artist 20', 240);")
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (3, 10);")
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (3, 20);")

	// 1. GetHierarchy
	tree, err := GetPlaylistHierarchy(ctx, database)
	if err != nil {
		t.Fatalf("GetPlaylistHierarchy failed: %v", err)
	}

	if len(tree) != 2 {
		t.Fatalf("expected 2 root nodes (EDM, Inbox), got %d", len(tree))
	}
	if tree[0].Title != "EDM" || tree[1].Title != "Inbox" {
		t.Errorf("unexpected root nodes: %v", tree)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Title != "House" {
		t.Errorf("expected child House, got %v", tree[0].Children)
	}
	if len(tree[0].Children[0].Children) != 1 || tree[0].Children[0].Children[0].TrackCount != 2 {
		t.Errorf("expected 2 tracks in Deep House, got %v", tree[0].Children[0].Children)
	}

	// 2. InspectPlaylist (Deep House)
	details, err := InspectPlaylist(ctx, database, 3)
	if err != nil {
		t.Fatalf("InspectPlaylist failed: %v", err)
	}
	if details.TrackCount != 2 || details.TotalDuration != 540 {
		t.Errorf("expected 2 tracks / 540s duration, got count=%d, duration=%d", details.TrackCount, details.TotalDuration)
	}

	// 3. Inspect non-existent playlist
	_, err = InspectPlaylist(ctx, database, 9999)
	if err == nil {
		t.Error("expected error for non-existent playlist ID")
	}
}

func TestPlaylist_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	if _, err := GetPlaylistHierarchy(ctx, nil); err == nil {
		t.Error("expected error for nil database")
	}
	if _, err := InspectPlaylist(ctx, nil, 1); err == nil {
		t.Error("expected error for nil database")
	}

	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	_ = database.Close()

	if _, err := GetPlaylistHierarchy(ctx, database); err == nil {
		t.Error("expected error on closed database")
	}
	if _, err := InspectPlaylist(ctx, database, 1); err == nil {
		t.Error("expected error on closed database")
	}
}
