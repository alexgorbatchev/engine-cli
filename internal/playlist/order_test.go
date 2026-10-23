package playlist

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestMovePlaylistBefore(t *testing.T) {
	for _, tc := range []struct {
		name, source, parent, before string
		wantErr                      bool
	}{
		{"before sibling", "2", "0", "1", false},
		{"missing sibling", "2", "0", "999", true},
		{"invalid sibling", "2", "0", "not-an-id", true},
		{"self parent", "1", "1", "", true},
		{"invalid parent", "2", "not-an-id", "", true},
		{"invalid source", "not-an-id", "0", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := db.CreateLibrary(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			for _, name := range []string{"One", "Two"} {
				if _, err := db.CreatePlaylist(t.Context(), d, name, 0, false); err != nil {
					t.Fatal(err)
				}
			}
			moved, parent, err := MovePlaylist(t.Context(), d, tc.source, tc.parent, tc.before)
			if (err != nil) != tc.wantErr {
				t.Fatalf("moved=%v parent=%v err=%v", moved, parent, err)
			}
			if !tc.wantErr {
				lists, err := GetPlaylistHierarchy(t.Context(), d)
				if err != nil || len(lists) != 2 || lists[0].ID != 2 || lists[1].ID != 1 || moved.ID != 2 || parent != nil {
					t.Fatalf("hierarchy=%v moved=%v parent=%v err=%v", lists, moved, parent, err)
				}
			}
		})
	}
}
