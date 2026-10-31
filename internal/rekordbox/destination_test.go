package rekordbox

import (
	"context"
	"errors"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestDestinationReviewRequiresNewRootAndRetainsNativeIdentity(t *testing.T) {
	ctx := context.Background()
	d := importDatabase(t)
	parent, err := db.CreatePlaylist(ctx, d, "Existing root", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePlaylist(ctx, d, "Reviewed root", parent, true); err != nil {
		t.Fatal(err)
	}
	uuid, err := destinationIdentity(ctx, d, "Reviewed root")
	if err != nil || uuid == "" {
		t.Fatalf("nested name blocked independent new root: %q, %v", uuid, err)
	}
	var nativeUUID string
	if err := d.QueryRow("SELECT uuid FROM Information WHERE id=1").Scan(&nativeUUID); err != nil {
		t.Fatal(err)
	}
	if uuid != nativeUUID {
		t.Fatalf("destination identity changed: %q != %q", uuid, nativeUUID)
	}
	if _, err := db.CreatePlaylist(ctx, d, "Reviewed root", 0, true); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if got, err := destinationIdentity(ctx, tx, "Reviewed root"); got != "" || progress.ErrorCode(err) != targetCode {
		t.Fatalf("colliding root became append target: %q, %v", got, err)
	}
}

func TestDestinationReviewRefusesUnavailableNativeEvidence(t *testing.T) {
	for _, name := range []string{"cancelled", "missing identity", "empty identity", "missing playlists"} {
		t.Run(name, func(t *testing.T) {
			d := importDatabase(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			query := ""
			switch name {
			case "missing identity":
				query = "DELETE FROM Information"
			case "empty identity":
				query = "UPDATE Information SET uuid=''"
			case "missing playlists":
				query = "DROP TABLE Playlist"
			}
			if query != "" {
				if _, err := d.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			got, err := destinationIdentity(ctx, d, "New root")
			if err == nil || got != "" {
				t.Fatalf("unavailable destination evidence accepted: %q, %v", got, err)
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
