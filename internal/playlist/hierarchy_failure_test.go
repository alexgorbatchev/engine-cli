package playlist

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"modernc.org/sqlite"
)

func TestHierarchyRejectsUnreadableTracks(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.InitSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Readable playlist',0); DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	lists, err := db.GetAllPlaylists(t.Context(), d)
	if err != nil || len(lists) != 1 {
		t.Fatalf("playlist enumeration failed: lists=%v err=%v", lists, err)
	}
	tree, err := GetPlaylistHierarchy(t.Context(), d)
	var cause *sqlite.Error
	if !errors.As(err, &cause) || !strings.Contains(err.Error(), "playlist 1") || tree != nil {
		t.Fatalf("unreadable tracks returned a hierarchy: tree=%v err=%v", tree, err)
	}
}
