package playlist

import (
	"context"
	"github.com/dj/engine-cli/internal/db"
	"path/filepath"
	"testing"
)

func TestRenamePlaylist(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Old',0)"); err != nil {
		t.Fatal(err)
	}
	p, err := RenamePlaylist(ctx, d, "1", "  New  ")
	if err != nil || p.Title != "New" || p.ID != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, tt := range []struct{ id, title string }{{"1", " "}, {"999", "New"}} {
		if _, err := RenamePlaylist(ctx, d, tt.id, tt.title); err == nil {
			t.Fatal("accepted invalid rename")
		}
	}
	if _, err := d.Exec("CREATE TRIGGER reject_rename BEFORE UPDATE ON Playlist BEGIN SELECT RAISE(ABORT,'reject'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := RenamePlaylist(ctx, d, "1", "Other"); err == nil {
		t.Fatal("ignored write failure")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RenamePlaylist(ctx, d, "1", "Other"); err == nil {
		t.Fatal("ignored database failure")
	}
}
