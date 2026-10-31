package intake

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func TestPortableQueueValidationPreservesDamagedEvidence(t *testing.T) {
	for _, test := range []struct{ name, damage string }{
		{"missing item projection", "ALTER TABLE Item RENAME TO RetainedItem"},
		{"missing receipt projection", "ALTER TABLE ImportIntent RENAME TO RetainedIntent"},
		{"missing control", "ALTER TABLE QueueControl RENAME TO RetainedControl"},
		{"foreign image", "PRAGMA application_id=0"},
		{"unreadable item value", "ALTER TABLE Item RENAME TO RetainedItem; CREATE VIEW Item AS SELECT id,NULL AS source_path,source_identity,source_size,source_modified,status,attempt,cancel_requested,stage_id,content_hash,error_code,history_id FROM RetainedItem"},
		{"unreadable receipt value", "ALTER TABLE ImportIntent RENAME TO RetainedIntent; CREATE VIEW ImportIntent AS SELECT NULL AS item_id,'history' AS history_id,'token' AS plan_token,'' AS to_playlist,NULL AS track_id"},
		{"invalid receipt identity", "ALTER TABLE ImportIntent RENAME TO RetainedIntent; CREATE VIEW ImportIntent AS SELECT 0 AS item_id,'history' AS history_id,'token' AS plan_token,'' AS to_playlist,NULL AS track_id"},
		{"orphan receipt", "PRAGMA foreign_keys=OFF; INSERT INTO ImportIntent(item_id,history_id,plan_token,to_playlist) VALUES(999,'history','token','')"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, library := queueFixture(t)
			if _, err := store.Enqueue(t.Context(), sourceFixture(t, "retained.wav")); err != nil {
				t.Fatal(err)
			}
			if _, err := store.database.ExecContext(t.Context(), test.damage); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(library, ".engine-cli", companion.IntakeName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateDatabase(t.Context(), path); err == nil {
				t.Fatal("damaged portable queue accepted for replacement")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("validation changed recovery evidence: equal=%v error=%v", bytes.Equal(before, after), err)
			}
		})
	}
}
