package history

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSmartlistPreviewNoEnrollmentAndReviewedImpacts(t *testing.T) {
	ctx := context.Background()
	lib, database := smartlistHistoryLibrary(t)
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || review == nil || len(review.Rows) != 0 || review.PlanToken == "" {
		t.Fatalf("healthy preview=%+v error=%v", review, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("healthy preview enrolled storage: %v", err)
	}
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	first, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || first.Reconnected != 1 || first.Removed != 0 || first.AmendedDeletes != 0 || len(first.Rows) != 1 || first.Rows[0].Action != "reconnect" || first.Rows[0].UUID != "saved" {
		t.Fatalf("rename preview=%+v error=%v", first, err)
	}
	second, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged preview changed consent: first=%+v second=%+v error=%v", first, second, err)
	}
	recordSmartlistMutation(t, lib, database, "playlist.rm", `DELETE FROM Playlist WHERE id=1`)
	removed, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || removed.Removed != 1 || removed.Reconnected != 0 || removed.AmendedDeletes != 1 || len(removed.Rows) != 1 || removed.Rows[0].DeleteEntryID == "" || removed.PlanToken == first.PlanToken {
		t.Fatalf("delete preview=%+v error=%v", removed, err)
	}
}

func TestSmartlistPreviewUnknownHistoryRemovalAndCancellation(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Unknown;' WHERE listUuid='saved'`); err != nil {
		t.Fatal(err)
	}
	review, err := PreviewSmartlistRepair(context.Background(), lib)
	if err != nil || review.Removed != 1 || review.AmendedDeletes != 0 || review.Rows[0].DeleteEntryID != "" {
		t.Fatalf("orphan preview=%+v error=%v", review, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("orphan preview enrolled storage: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := PreviewSmartlistRepair(ctx, lib); err == nil || result != nil {
		t.Fatalf("cancelled preview=%+v error=%v", result, err)
	}
}
