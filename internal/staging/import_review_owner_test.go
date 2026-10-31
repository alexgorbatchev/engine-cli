package staging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestImportReviewResolvesPlaylistAndExcludesMixContainer(t *testing.T) {
	store, database, item, _ := importReviewFixture(t)
	if _, err := database.Exec("INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(7,'Imports',0,0)"); err != nil {
		t.Fatal(err)
	}
	mix, err := store.Create(t.Context(), Item{Kind: "mix", Status: "ready", Title: "Container"})
	if err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{IDs: []string{mix.ID, item.ID}, ToPlaylist: "7"}
	review, err := store.ReviewImport(t.Context(), database, opts)
	if err != nil || review == nil || review.Ready != 1 || review.Skipped != 0 || len(review.Items) != 1 || review.Items[0].ID != item.ID {
		t.Fatalf("mix container was treated as importable media: %+v %v", review, err)
	}
	if _, err := store.CheckImport(t.Context(), database, opts, review.PlanToken); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Playlist SET title='Changed' WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.CheckImport(t.Context(), database, opts, review.PlanToken); got != nil || err == nil {
		t.Fatalf("renamed destination accepted old review: %+v %v", got, err)
	}
}

func TestCancelledImportPreparationNeverCallsInstallationIntent(t *testing.T) {
	store, database, item, _ := importReviewFixture(t)
	opts := ImportOptions{IDs: []string{item.ID}}
	review, err := store.ReviewImport(t.Context(), database, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := store.ReviewImport(ctx, database, opts); got != nil || err == nil {
		t.Fatalf("cancelled review accepted: %+v %v", got, err)
	}
	called := false
	if got, err := store.ImportRetained(ctx, database, opts, review.PlanToken, func(context.Context) error { called = true; return nil }); got != nil || err == nil || called {
		t.Fatalf("cancelled import reached intent: %+v %v called=%v", got, err, called)
	}
	if _, err := os.Stat(filepath.Join(store.lib, filepath.FromSlash(review.Items[0].Destination))); !os.IsNotExist(err) {
		t.Fatalf("cancelled import installed media: %v", err)
	}
}
