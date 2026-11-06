package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestSmartlistProjectionDigestBindsOriginalEvidence(t *testing.T) {
	for _, change := range []string{"history bytes", "omitted relationship", "omitted invalid UTF-8 rules"} {
		t.Run(change, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			if change != "history bytes" {
				if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Missing;',rules=CAST(? AS TEXT)`, []byte{0xff}); err != nil {
					t.Fatal(err)
				}
			}
			before := projectionForTest(t, lib, database)
			switch change {
			case "history bytes":
				path := filepath.Join(lib, ".engine-cli", "history", entry.ID+".json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var value map[string]any
				if err := json.Unmarshal(data, &value); err != nil {
					t.Fatal(err)
				}
				value["future_evidence"] = "changed"
				data, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "omitted relationship":
				if _, err := database.Exec(`UPDATE Smartlist SET parentPlaylistPath='Different missing;'`); err != nil {
					t.Fatal(err)
				}
			case "omitted invalid UTF-8 rules":
				if _, err := database.Exec(`UPDATE Smartlist SET rules=CAST(? AS TEXT)`, []byte{0xfe}); err != nil {
					t.Fatal(err)
				}
			}
			after := projectionForTest(t, lib, database)
			if !reflect.DeepEqual(before.Rows, after.Rows) || !reflect.DeepEqual(before.Actions, after.Actions) || before.EvidenceDigest == after.EvidenceDigest {
				t.Fatalf("changed evidence did not independently invalidate digest: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestSmartlistProjectionRefusesDamagedHistory(t *testing.T) {
	for _, damage := range []string{"incomplete operation", "missing image hash", "changed native image", "malformed entry"} {
		t.Run(damage, func(t *testing.T) {
			lib, database := smartlistHistoryLibrary(t)
			entry := recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lib, "Database2", "m.db")
			switch damage {
			case "incomplete operation":
				entry.After = nil
			case "missing image hash":
				state := entry.Before.Databases[path]
				state.Hash = ""
				entry.Before.Databases[path] = state
			case "changed native image":
				image, err := db.Open(entry.Before.Databases[path].Path)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := image.Exec(`UPDATE Smartlist SET rules='changed'`)
				if err := errors.Join(writeErr, image.Close()); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeEntry(dir, entry); err != nil {
				t.Fatal(err)
			}
			if damage == "malformed entry" {
				if err := os.WriteFile(filepath.Join(dir, entry.ID+".json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := historyLeaseTree(t, lib)
			called, err := ReadSmartlistProjection(t.Context(), lib, database, func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error) { return true, nil })
			if err == nil || called {
				t.Fatalf("accepted %s: callback=%v err=%v", damage, called, err)
			}
			if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed or recovered source evidence:\n%s", historyLeaseTreeDiff(before, after))
			}
		})
	}
}

func TestSmartlistProjectionSharesNativeSnapshotAndReleasesTransaction(t *testing.T) {
	lib, writer := smartlistHistoryLibrary(t)
	if _, err := writer.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	reader, err := db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var observed *sql.Tx
	projection, err := ReadSmartlistProjection(t.Context(), lib, reader, func(ctx context.Context, tx *sql.Tx, projection *SmartlistProjection) (*SmartlistProjection, error) {
		observed = tx
		if _, err := writer.Exec(`UPDATE Playlist SET title='Later' WHERE id=1; UPDATE Smartlist SET parentPlaylistPath='Later;',rules='later rules'`); err != nil {
			return nil, err
		}
		var title, rules string
		if err := tx.QueryRowContext(ctx, `SELECT title FROM Playlist WHERE id=1`).Scan(&title); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT rules FROM Smartlist WHERE listUuid='saved'`).Scan(&rules); err != nil {
			return nil, err
		}
		if title != "House" || rules != "opaque" || projection.Rows[0].Values["parentPlaylistPath"] != "House;" {
			t.Fatalf("mixed native snapshots: title=%q rules=%q projection=%+v", title, rules, projection)
		}
		return projection, nil
	})
	if err != nil || projection == nil {
		t.Fatalf("snapshot observation=%+v err=%v", projection, err)
	}
	if err := observed.QueryRow(`SELECT 1`).Scan(new(int)); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("callback transaction escaped its owner: %v", err)
	}
	after := projectionForTest(t, lib, reader)
	if after.Rows[0].Values["parentPlaylistPath"] != "Later;" || after.EvidenceDigest == projection.EvidenceDigest {
		t.Fatalf("later observation did not see committed native change: %+v", after)
	}
}
