package details

import (
	"context"
	"github.com/dj/engine-cli/internal/db"
	"path/filepath"
	"testing"
)

func TestBatchDetails(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title) VALUES(1,'Same'),(2,'Same'); INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Same',0),(2,'Same',1)`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"track", "playlist"} {
		r, err := Read(ctx, d, kind, []int64{2, 999, 1, 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Items) != 4 || r.Items[0].ID != 2 || r.Items[0].Data["title"] != "Same" || r.Items[1].Error.Code != "not_found" || r.Items[2].ID != 1 || r.Items[3].ID != 2 {
			t.Fatalf("%+v", r)
		}
	}
	for _, ids := range [][]int64{nil, {0}, {-1}, make([]int64, MaxItems+1)} {
		if err := Validate(ids); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
	if _, err := Read(ctx, d, "invalid", []int64{1}); err == nil {
		t.Fatal("accepted invalid kind")
	}
	if _, err := Read(ctx, d, "track", nil); err == nil {
		t.Fatal("accepted empty ids")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Read(cancelled, d, "track", []int64{1}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"track", "playlist"} {
		if _, err := Read(ctx, d, kind, []int64{1}); err == nil {
			t.Fatal("ignored db failure")
		}
	}
}
