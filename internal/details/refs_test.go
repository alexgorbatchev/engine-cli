package details

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

func TestReferenceDetails(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	d, err := db.Open(filepath.Join(lib, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title) VALUES(1,'Track'); INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Set',0);INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	entities, err := identity.List(ctx, d, lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		r, err := ReadRefs(ctx, d, nil, e.Kind, []string{e.Ref, "eng:missing:" + e.Kind + ":missing", e.Ref})
		if err != nil || len(r.Items) != 3 || r.Items[0].ID != 1 || r.Items[0].Ref != e.Ref || r.Items[1].Error == nil || r.Items[2].Ref != e.Ref {
			t.Fatalf("reference details: %+v %v", r, err)
		}
		numeric, err := Read(ctx, d, nil, e.Kind, []int64{1})
		if err != nil || numeric.Items[0].Ref != e.Ref {
			t.Fatalf("numeric enrichment: %+v %v", numeric, err)
		}
	}
	for _, refs := range [][]string{nil, {"bad"}, make([]string, 501)} {
		if err := ValidateRefs(refs); err == nil {
			t.Fatal("invalid refs accepted")
		}
	}
	for _, kind := range []string{"bad", "track"} {
		if _, err := ReadRefs(ctx, d, nil, kind, []string{"bad"}); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ReadRefs(ctx, d, nil, "track", []string{entities[0].Ref}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
