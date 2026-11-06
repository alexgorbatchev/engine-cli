package intake

import (
	"database/sql"
	"errors"
	"testing"
)

func TestWatchHandoffNativeWriteRefusalsPreserveRecoverableIntent(t *testing.T) {
	for _, boundary := range []string{"journal", "queue", "link"} {
		t.Run(boundary, func(t *testing.T) {
			s, _ := queueFixture(t)
			selected, version := watchSourceFixture(t, s)
			d, err := openWatch(t.Context(), s.library)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			target := d
			statement := "CREATE TRIGGER RefuseHandoff BEFORE INSERT ON WatchHandoff BEGIN SELECT RAISE(ABORT,'journal storage refused'); END"
			switch boundary {
			case "queue":
				target = s.database
				statement = "CREATE TRIGGER RefuseHandoff BEFORE INSERT ON Item BEGIN SELECT RAISE(ABORT,'queue storage refused'); END"
			case "link":
				statement = "CREATE TRIGGER RefuseHandoff BEFORE UPDATE ON WatchHandoff BEGIN SELECT RAISE(ABORT,'link storage refused'); END"
			}
			if _, err := target.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			_, _, published, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			if err == nil || published {
				t.Fatalf("failed native publication reported success: %v %v", published, err)
			}
			items, err := s.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantItems := 0
			if boundary == "link" {
				wantItems = 1
			}
			if len(items) != wantItems {
				t.Fatalf("wrong partial-publication boundary: %+v", items)
			}
			handoff, err := readWatchHandoff(t.Context(), d, version)
			if boundary == "journal" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("refused journal left receipt: %+v %v", handoff, err)
				}
			} else if err != nil || handoff.ItemID.Valid || handoff.Provenance != "pending" {
				t.Fatalf("failed publication lost pending intent: %+v %v", handoff, err)
			}
			if _, err := target.ExecContext(t.Context(), "DROP TRIGGER RefuseHandoff"); err != nil {
				t.Fatal(err)
			}
			item, provenance, published, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			if err != nil || item == nil || !published {
				t.Fatalf("publication did not recover: %+v %s %v %v", item, provenance, published, err)
			}
			if boundary == "link" && provenance != "unknown" {
				t.Fatalf("recovery invented creator provenance: %s", provenance)
			}
			if boundary != "link" && provenance != "created" {
				t.Fatalf("successful fresh insertion lost creator provenance: %s", provenance)
			}
			items, err = s.List(t.Context())
			if err != nil || len(items) != 1 || items[0].ID != item.ID {
				t.Fatalf("recovery duplicated queue item: %+v %v", items, err)
			}
			if err := selected.Check(); err != nil {
				t.Fatal("storage refusal changed native source:", err)
			}
		})
	}
}

func TestWatchHandoffRefusesConflictingDurableReceipt(t *testing.T) {
	for _, damaged := range []string{"captured-source", "linked-item"} {
		t.Run(damaged, func(t *testing.T) {
			s, _ := queueFixture(t)
			selected, version := watchSourceFixture(t, s)
			item, _, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			if err != nil {
				t.Fatal(err)
			}
			d, err := openWatch(t.Context(), s.library)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			if damaged == "captured-source" {
				_, err = d.ExecContext(t.Context(), "UPDATE WatchHandoff SET source_identity=source_identity||'-conflicting' WHERE version_id=?", version)
			} else {
				_, err = s.database.ExecContext(t.Context(), "UPDATE Item SET source_path=source_path||'-conflicting' WHERE id=?", item.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, _, published, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			watchCode(t, err, "watch_recovery_required")
			if published {
				t.Fatal("conflicting durable receipt was republished")
			}
			items, err := s.List(t.Context())
			if err != nil || len(items) != 1 || items[0].ID != item.ID {
				t.Fatalf("conflicting receipt duplicated/replaced item: %+v %v", items, err)
			}
			if err := selected.Check(); err != nil {
				t.Fatal("conflicting receipt changed native source:", err)
			}
		})
	}
}
