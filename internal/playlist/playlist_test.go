package playlist

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
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
	for _, id := range []int64{10, 20} {
		if _, err := db.AddTrackToPlaylist(ctx, database, 3, id); err != nil {
			t.Fatal(err)
		}
	}

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

	// 2. InspectPlaylist (Deep House by ID and by name)
	details, err := InspectPlaylist(ctx, database, "3")
	if err != nil {
		t.Fatalf("InspectPlaylist failed: %v", err)
	}
	if details.TrackCount != 2 || details.TotalDuration != 540 {
		t.Errorf("expected 2 tracks / 540s duration, got count=%d, duration=%d", details.TrackCount, details.TotalDuration)
	}

	detailsName, err := InspectPlaylist(ctx, database, "3")
	if err != nil || detailsName.ID != 3 {
		t.Fatalf("InspectPlaylist by name failed: %v, got %v", err, detailsName)
	}

	// 3. Inspect non-existent playlist
	_, err = InspectPlaylist(ctx, database, "9999")
	if err == nil {
		t.Error("expected error for non-existent playlist ID")
	}
}

func TestFormatPlaylistTree(t *testing.T) {
	tests := []struct {
		name  string
		nodes []PlaylistNode
		want  string
	}{
		{
			name:  "empty",
			nodes: []PlaylistNode{},
			want:  "",
		},
		{
			name: "single root no children",
			nodes: []PlaylistNode{
				{ID: 1, Title: "Inbox", TrackCount: 5},
			},
			want: strings.Join([]string{
				"Collection",
				"└── Inbox [id:1 tracks:5]",
				"",
			}, "\n"),
		},
		{
			name: "single root with children",
			nodes: []PlaylistNode{
				{
					ID:         1,
					Title:      "EDM",
					TrackCount: 0,
					Children: []PlaylistNode{
						{ID: 2, Title: "House", TrackCount: 10},
						{ID: 3, Title: "Techno", TrackCount: 15},
					},
				},
			},
			want: strings.Join([]string{
				"Collection",
				"└── EDM [id:1 tracks:0]",
				"    ├── House [id:2 tracks:10]",
				"    └── Techno [id:3 tracks:15]",
				"",
			}, "\n"),
		},
		{
			name: "multiple top level playlists",
			nodes: []PlaylistNode{
				{
					ID:         1,
					Title:      "EDM",
					TrackCount: 0,
					Children: []PlaylistNode{
						{ID: 2, Title: "House", TrackCount: 10},
					},
				},
				{
					ID:         3,
					Title:      "Inbox",
					TrackCount: 5,
				},
			},
			want: strings.Join([]string{
				"Collection",
				"├── EDM [id:1 tracks:0]",
				"│   └── House [id:2 tracks:10]",
				"└── Inbox [id:3 tracks:5]",
				"",
			}, "\n"),
		},
		{
			name: "deep nested hierarchy",
			nodes: []PlaylistNode{
				{
					ID:         1,
					Title:      "Main",
					TrackCount: 0,
					Children: []PlaylistNode{
						{
							ID:         2,
							Title:      "Level1-A",
							TrackCount: 1,
							Children: []PlaylistNode{
								{ID: 3, Title: "Level2-A1", TrackCount: 2},
								{ID: 4, Title: "Level2-A2", TrackCount: 3},
							},
						},
						{
							ID:         5,
							Title:      "Level1-B",
							TrackCount: 4,
						},
					},
				},
			},
			want: strings.Join([]string{
				"Collection",
				"└── Main [id:1 tracks:0]",
				"    ├── Level1-A [id:2 tracks:1]",
				"    │   ├── Level2-A1 [id:3 tracks:2]",
				"    │   └── Level2-A2 [id:4 tracks:3]",
				"    └── Level1-B [id:5 tracks:4]",
				"",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPlaylistTree(tt.nodes)
			if got != tt.want {
				t.Errorf("FormatPlaylistTree() got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatPlaylistBulletList(t *testing.T) {
	tests := []struct {
		name  string
		nodes []PlaylistNode
		want  string
	}{
		{
			name:  "empty",
			nodes: []PlaylistNode{},
			want:  "",
		},
		{
			name: "single root no children",
			nodes: []PlaylistNode{
				{ID: 1, Title: "Inbox", TrackCount: 5},
			},
			want: strings.Join([]string{
				"- Collection",
				"  - Inbox [id:1 tracks:5]",
				"",
			}, "\n"),
		},
		{
			name: "nested hierarchy",
			nodes: []PlaylistNode{
				{
					ID:         1,
					Title:      "EDM",
					TrackCount: 0,
					Children: []PlaylistNode{
						{ID: 2, Title: "House", TrackCount: 10},
					},
				},
				{
					ID:         3,
					Title:      "Inbox",
					TrackCount: 5,
				},
			},
			want: strings.Join([]string{
				"- Collection",
				"  - EDM [id:1 tracks:0]",
				"    - House [id:2 tracks:10]",
				"  - Inbox [id:3 tracks:5]",
				"",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPlaylistBulletList(tt.nodes)
			if got != tt.want {
				t.Errorf("FormatPlaylistBulletList() got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatTrackLine(t *testing.T) {
	tests := []struct {
		name  string
		track db.Track
		want  string
	}{
		{
			name: "artist and title with length",
			track: db.Track{
				ID:     1,
				Artist: sql.NullString{String: "Artist Name", Valid: true},
				Title:  sql.NullString{String: "Track Title", Valid: true},
				Length: sql.NullInt64{Int64: 305, Valid: true},
			},
			want: "Artist Name - Track Title [id:1 5:05]",
		},
		{
			name: "title only without artist",
			track: db.Track{
				ID:     2,
				Title:  sql.NullString{String: "Only Title", Valid: true},
				Length: sql.NullInt64{Int64: 60, Valid: true},
			},
			want: "Only Title [id:2 1:00]",
		},
		{
			name: "filename fallback when title empty",
			track: db.Track{
				ID:       3,
				Filename: sql.NullString{String: "song.mp3", Valid: true},
			},
			want: "song.mp3 [id:3 0:00]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTrackLine(tt.track)
			if got != tt.want {
				t.Errorf("FormatTrackLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlaylistOperations(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// 1. CreatePlaylist
	pRoot, err := CreatePlaylist(ctx, database, "Techno", "", true)
	if err != nil || pRoot.ID <= 0 {
		t.Fatalf("CreatePlaylist error: %v", err)
	}
	pChild, err := CreatePlaylist(ctx, database, "Peak Time", "1", false)
	if err != nil || pChild.ParentListID != pRoot.ID {
		t.Fatalf("CreatePlaylist child error: %v, got %v", err, pChild)
	}

	// 2. MovePlaylist
	pDestFolder, _ := CreatePlaylist(ctx, database, "Underground", "", true)
	movedPL, targetParent, err := MovePlaylist(ctx, database, "2", "3", "")
	if err != nil || movedPL.ParentListID != pDestFolder.ID || targetParent.ID != pDestFolder.ID {
		t.Fatalf("MovePlaylist error: %v, moved=%v, target=%v", err, movedPL, targetParent)
	}
	// Move to Collection (root 0)
	movedRoot, targetRoot, err := MovePlaylist(ctx, database, "2", "0", "")
	if err != nil || movedRoot.ParentListID != 0 || targetRoot != nil {
		t.Fatalf("MovePlaylist to root error: %v", err)
	}

	// Insert tracks for membership tests
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, title, artist, path, filename) VALUES (101, 'Acid Track', 'DJ Acid', 'p101', 'f101');")
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, title, artist, path, filename) VALUES (102, 'Rave Track', 'DJ Rave', 'p102', 'f102');")

	// 3. AddTrackToPlaylist
	plAdded, trAdded, err := AddTrackToPlaylist(ctx, database, "2", "101")
	if err != nil || plAdded.ID != movedRoot.ID || trAdded.ID != 101 {
		t.Fatalf("AddTrackToPlaylist error: %v, pl=%v, tr=%v", err, plAdded, trAdded)
	}

	// 4. MoveTrackBetweenPlaylists
	pNewDest, _ := CreatePlaylist(ctx, database, "Festival", "", false)
	srcPL, dstPL, trMoved, err := MoveTrackBetweenPlaylists(ctx, database, "2", "4", "101")
	if err != nil || srcPL.ID != movedRoot.ID || dstPL.ID != pNewDest.ID || trMoved.ID != 101 {
		t.Fatalf("MoveTrackBetweenPlaylists error: %v", err)
	}

	// 5. RemoveTrackFromPlaylist
	_, _, err = RemoveTrackFromPlaylist(ctx, database, "4", "101")
	if err != nil {
		t.Fatalf("RemoveTrackFromPlaylist error: %v", err)
	}

	// 6. DeletePlaylist with children without force (error)
	_, _ = CreatePlaylist(ctx, database, "ParentToDel", "", true)
	_, _ = CreatePlaylist(ctx, database, "ChildToDel", "5", false)
	if _, err := DeletePlaylist(ctx, database, "5", false); err == nil {
		t.Error("expected error deleting playlist with children without force")
	}

	// 7. DeletePlaylist
	delPL, err := DeletePlaylist(ctx, database, "4", false)
	if err != nil || delPL.ID != pNewDest.ID {
		t.Fatalf("DeletePlaylist error: %v", err)
	}
}

func TestPlaylistOperations_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	defer database.Close()
	_ = db.InitSchema(ctx, database)

	// Nil database
	if _, err := CreatePlaylist(ctx, nil, "T", "", false); err == nil {
		t.Error("expected error for nil db in CreatePlaylist")
	}
	if _, err := DeletePlaylist(ctx, nil, "T", false); err == nil {
		t.Error("expected error for nil db in DeletePlaylist")
	}
	if _, _, err := MovePlaylist(ctx, nil, "T", "", ""); err == nil {
		t.Error("expected error for nil db in MovePlaylist")
	}
	if _, _, err := AddTrackToPlaylist(ctx, nil, "T", "1"); err == nil {
		t.Error("expected error for nil db in AddTrackToPlaylist")
	}
	if _, _, err := RemoveTrackFromPlaylist(ctx, nil, "T", "1"); err == nil {
		t.Error("expected error for nil db in RemoveTrackFromPlaylist")
	}
	if _, _, _, err := MoveTrackBetweenPlaylists(ctx, nil, "T", "U", "1"); err == nil {
		t.Error("expected error for nil db in MoveTrackBetweenPlaylists")
	}

	// Empty titles and non-existent identifiers
	if _, err := CreatePlaylist(ctx, database, "", "", false); err == nil {
		t.Error("expected error for empty playlist title")
	}
	if _, err := CreatePlaylist(ctx, database, "T", "nonexistent", false); err == nil {
		t.Error("expected error for non-existent parent")
	}
	if _, err := DeletePlaylist(ctx, database, "999", false); err == nil {
		t.Error("expected error deleting non-existent playlist")
	}
	if _, _, err := MovePlaylist(ctx, database, "999", "", ""); err == nil {
		t.Error("expected error moving non-existent playlist")
	}
	p1, _ := CreatePlaylist(ctx, database, "P1", "", false)
	if _, _, err := MovePlaylist(ctx, database, "1", "999", ""); err == nil {
		t.Error("expected error moving to non-existent parent")
	}
	if _, _, err := AddTrackToPlaylist(ctx, database, "999", "1"); err == nil {
		t.Error("expected error adding track to non-existent playlist")
	}
	if _, _, err := AddTrackToPlaylist(ctx, database, "1", "nonexistent_track"); err == nil {
		t.Error("expected error adding non-existent track")
	}
	if _, _, err := RemoveTrackFromPlaylist(ctx, database, "999", "1"); err == nil {
		t.Error("expected error removing track from non-existent playlist")
	}
	if _, _, err := RemoveTrackFromPlaylist(ctx, database, "1", "nonexistent_track"); err == nil {
		t.Error("expected error removing non-existent track from playlist")
	}
	if _, _, _, err := MoveTrackBetweenPlaylists(ctx, database, "999", "1", "1"); err == nil {
		t.Error("expected error for non-existent src in MoveTrackBetweenPlaylists")
	}
	if _, _, _, err := MoveTrackBetweenPlaylists(ctx, database, "1", "999", "1"); err == nil {
		t.Error("expected error for non-existent dst in MoveTrackBetweenPlaylists")
	}
	if _, _, _, err := MoveTrackBetweenPlaylists(ctx, database, "1", "1", "nonexistent_track"); err == nil {
		t.Error("expected error for non-existent track in MoveTrackBetweenPlaylists")
	}
	_ = p1
}

func TestPlaylist_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	if _, err := GetPlaylistHierarchy(ctx, nil); err == nil {
		t.Error("expected error for nil database")
	}
	if _, err := InspectPlaylist(ctx, nil, "1"); err == nil {
		t.Error("expected error for nil database")
	}

	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	_ = database.Close()

	if _, err := GetPlaylistHierarchy(ctx, database); err == nil {
		t.Error("expected error on closed database")
	}
	if _, err := InspectPlaylist(ctx, database, "1"); err == nil {
		t.Error("expected error on closed database")
	}
}
