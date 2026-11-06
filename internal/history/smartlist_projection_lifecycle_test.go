package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistProjectionPendingRepairDoesNotReadOrRecover(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	pending := filepath.Join(lib, ".engine-cli", "smartlist-repair", "pending.json")
	if err := os.MkdirAll(filepath.Dir(pending), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	before := historyLeaseTree(t, lib)
	called, err := ReadSmartlistProjection(t.Context(), lib, database, func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error) { return true, nil })
	if called || progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("pending repair observation callback=%v error=%v", called, err)
	}
	if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
		t.Fatalf("pending repair observation created lock storage or recovered evidence:\n%s", historyLeaseTreeDiff(before, after))
	}
}

func TestSmartlistProjectionFailureReleasesOwners(t *testing.T) {
	for _, failure := range []string{"callback", "cancelled", "closed database"} {
		t.Run(failure, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			}
			if failure == "closed database" {
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			}
			rejected := errors.New("reader rejected observation")
			var observed *sql.Tx
			_, err := ReadSmartlistProjection(ctx, lib, database, func(_ context.Context, tx *sql.Tx, _ *SmartlistProjection) (bool, error) {
				observed = tx
				return false, rejected
			})
			if err == nil || (failure == "callback" && !errors.Is(err, rejected)) {
				t.Fatalf("failure was swallowed: %v", err)
			}
			if observed != nil {
				if err := observed.QueryRow(`SELECT 1`).Scan(new(int)); !errors.Is(err, sql.ErrTxDone) {
					t.Fatalf("failed callback retained native transaction: %v", err)
				}
			}
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := lock(t.Context(), dir)
			if err != nil {
				t.Fatalf("failed observation retained history exclusion: %v", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
