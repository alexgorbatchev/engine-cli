package intake

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestOpenQueueRefusesUnreadableProjectionAndRecovers(t *testing.T) {
	s, library := queueFixture(t)
	d := importCollectionFixture(t, library)
	source := sourceFixture(t, "retained.wav")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("initial claim: %+v %v", claimed, err)
	}
	// Damage the live projection without deleting the retained attempt or its
	// source. A Store already open must refuse it just as replacement validation
	// does, before returning queue content, review consent or staged media.
	_, err = s.database.ExecContext(t.Context(), `ALTER TABLE Item RENAME TO RetainedItem;
CREATE VIEW Item AS SELECT id,NULL AS source_path,source_identity,source_size,source_modified,status,attempt,cancel_requested,stage_id,content_hash,error_code,history_id FROM RetainedItem`)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		run  func(*testing.T) error
	}{
		{"list", func(t *testing.T) error {
			items, err := s.List(t.Context())
			if items != nil {
				t.Fatalf("unreadable queue exposed content: %+v", items)
			}
			return err
		}},
		{"import review", func(t *testing.T) error {
			review, err := s.ReviewImport(t.Context(), d, ImportOptions{ID: claimed.ID})
			if review != nil {
				t.Fatalf("unreadable queue exposed import consent: %+v", review)
			}
			return err
		}},
		{"abandoned preparation recovery", func(t *testing.T) error {
			item, err := s.PrepareNext(t.Context(), 0)
			if item != nil {
				t.Fatalf("unreadable queue prepared media: %+v", item)
			}
			return err
		}},
		{"source selection", func(t *testing.T) error {
			selected, err := s.selectPreparationSource(t.Context(), claimed)
			if selected != nil {
				if err := selected.close(); err != nil {
					t.Error(err)
				}
				t.Fatal("unreadable queue authorized source selection")
			}
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(t); err == nil || progress.ErrorCode(err) != "operation_failed" {
				t.Fatalf("damaged projection refusal: %v", err)
			}
			retained, err := scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM RetainedItem WHERE id=?", claimed.ID))
			if err != nil || !reflect.DeepEqual(retained, claimed) {
				t.Fatalf("refusal changed retained attempt: %+v %v", retained, err)
			}
			items, err := staging.New(library).List(t.Context())
			if err != nil || len(items) != 0 {
				t.Fatalf("refusal published staged media: %+v %v", items, err)
			}
			data, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("refusal changed source: %q %v", data, err)
			}
		})
	}
	if _, err := s.database.ExecContext(t.Context(), "DROP VIEW Item; ALTER TABLE RetainedItem RENAME TO Item"); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.PrepareNext(t.Context(), 0)
	if err != nil || recovered == nil || recovered.ID != claimed.ID || recovered.Status != "staged" || recovered.Attempt != claimed.Attempt+1 || recovered.StageID != claimed.StageID || recovered.ContentHash == "" || recovered.HistoryID != "" {
		t.Fatalf("restored projection did not recover retained attempt: %+v %v", recovered, err)
	}
	path, err := staging.New(library).Resolve(t.Context(), recovered.StageID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, path} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("recovery lost retained media %s: %q %v", path, data, err)
		}
	}
}
