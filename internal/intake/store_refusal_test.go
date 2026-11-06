package intake

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
)

func TestQueueRefusesIncompleteStorageWithoutChangingEvidence(t *testing.T) {
	for _, test := range []struct{ name, damage string }{
		{"item", "ALTER TABLE Item RENAME TO RetainedItem"},
		{"intent", "ALTER TABLE ImportIntent RENAME TO RetainedIntent"},
		{"control", "ALTER TABLE QueueControl RENAME TO RetainedControl"},
		{"foreign", "PRAGMA application_id=0; PRAGMA user_version=0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			library := t.TempDir()
			s, err := Open(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := s.Enqueue(t.Context(), sourceFixture(t, "retained.wav")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.database.ExecContext(t.Context(), test.damage); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(library, ".engine-cli", companion.IntakeName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(t.Context(), library)
			if opened != nil {
				if closeErr := opened.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("incomplete storage accepted as a live queue")
			}
			if err == nil {
				t.Fatal("incomplete storage refused without an error")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed durable evidence: equal=%v error=%v", bytes.Equal(before, after), err)
			}
			// Failed initialization must close both the database and its lifetime
			// observation, so recovery can acquire exclusive replacement ownership.
			owner, err := intakelease.Replace(t.Context(), library)
			if err != nil {
				t.Fatalf("refusal stranded replacement ownership: %v", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
