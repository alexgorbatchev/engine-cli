package intake

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestMalformedWatchImageCannotAuthorizeRetainedAlias(t *testing.T) {
	for _, damage := range []struct{ name, apply, restore string }{
		{"unsupported-version", fmt.Sprintf("PRAGMA user_version=%d", watchSchemaVersion+1), fmt.Sprintf("PRAGMA user_version=%d", watchSchemaVersion)},
		{"invalid-control", "PRAGMA ignore_check_constraints=ON; UPDATE WatchControl SET reason='unrecognized'", "UPDATE WatchControl SET reason=''"},
		{"unreadable-root", "ALTER TABLE WatchRoot RENAME TO RetainedRoot; CREATE VIEW WatchRoot AS SELECT sequence,id,NULL AS path,identity,recursive,enabled,retired,phase,last_scan_at,error_code,observed FROM RetainedRoot", "DROP VIEW WatchRoot; ALTER TABLE RetainedRoot RENAME TO WatchRoot"},
		{"truncated-handoff", "ALTER TABLE WatchHandoff RENAME TO RetainedHandoff; CREATE VIEW WatchHandoff AS SELECT version_id,item_id FROM RetainedHandoff", "DROP VIEW WatchHandoff; ALTER TABLE RetainedHandoff RENAME TO WatchHandoff"},
		{"truncated-alias", "ALTER TABLE WatchAlias RENAME TO RetainedAlias; CREATE VIEW WatchAlias AS SELECT version_id,root_id FROM RetainedAlias", "DROP VIEW WatchAlias; ALTER TABLE RetainedAlias RENAME TO WatchAlias"},
	} {
		t.Run(damage.name, func(t *testing.T) {
			s, _ := queueFixture(t)
			source, version := watchSourceFixture(t, s)
			if _, err := s.Enqueue(t.Context(), source.source.Path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(source.source.Path, source.source.Path+"-alias.wav"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.reconcileWatch(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			claimed, err := s.Claim(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			selected, err := s.selectPreparationSource(t.Context(), claimed)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := selected.close(); err != nil {
					t.Error(err)
				}
			})
			d, err := openWatch(t.Context(), s.library)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := d.ExecContext(t.Context(), damage.apply); err != nil {
				t.Fatal(err)
			}
			path, err := companion.Database(s.library, companion.WatchName)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if damage.name == "unsupported-version" {
				opened, err := openWatch(t.Context(), s.library)
				if opened != nil {
					if err := opened.Close(); err != nil {
						t.Error(err)
					}
				}
				if err == nil || opened != nil {
					t.Fatalf("unsupported image enrolled writable watch authority: %v %v", opened, err)
				}
			}
			if other, err := s.selectPreparationSource(t.Context(), claimed); err == nil || other != nil {
				if other != nil {
					if err := other.close(); err != nil {
						t.Error(err)
					}
				}
				t.Fatalf("damaged authority selected alias: %v %v", other, err)
			}
			if err := intakestate.With(t.Context(), s.library, selected.authorize); err == nil {
				t.Fatal("damaged authority authorized retained alias")
			}
			aliasSource := source.source
			aliasSource.Path += "-alias.wav"
			if _, _, published, err := s.publishWatchHandoff(t.Context(), version, aliasSource, 0, selected.local.Check); err == nil || published {
				t.Fatalf("damaged authority accepted handoff: %v %v", published, err)
			}
			if damage.name == "unsupported-version" || damage.name == "invalid-control" || damage.name == "unreadable-root" {
				if _, err := s.WatchActivate(t.Context(), 0); err == nil {
					t.Fatal("damaged authority accepted activation")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed damaged recovery image: %v", err)
			}
			if _, err := d.ExecContext(t.Context(), damage.restore); err != nil {
				t.Fatal(err)
			}
			if err := intakestate.With(t.Context(), s.library, selected.authorize); err != nil {
				t.Fatal("repaired image did not restore original authorization:", err)
			}
			current, err := scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM Item WHERE id=?", claimed.ID))
			if err != nil || !reflect.DeepEqual(claimed, current) {
				t.Fatalf("damaged authority changed original attempt: %+v %v", current, err)
			}
		})
	}
}
