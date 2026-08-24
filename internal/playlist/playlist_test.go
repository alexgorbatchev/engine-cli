package playlist

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
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
