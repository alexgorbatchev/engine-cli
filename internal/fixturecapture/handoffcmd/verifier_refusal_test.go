package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestNativeVerifierRefusesUnavailableProjectionsAndRecovers(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	root := t.TempDir()
	f, err := seedFixture(t.Context(), root, in, g, "healthy")
	if err != nil {
		t.Fatal(err)
	}
	verify := func() error { return verifyOne(t.Context(), root, g, f, "seed") }
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, f.Name, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, table := range []string{"Information", "Track", "PlaylistEntity"} {
		t.Run(table, func(t *testing.T) {
			if _, err := d.ExecContext(t.Context(), "ALTER TABLE "+table+" RENAME TO WithheldProjection"); err != nil {
				t.Fatal(err)
			}
			refusal := verify()
			if _, err := d.ExecContext(t.Context(), "ALTER TABLE WithheldProjection RENAME TO "+table); err != nil {
				t.Fatal(err)
			}
			if refusal == nil || !strings.Contains(refusal.Error(), table) {
				t.Fatalf("missing native %s projection accepted: %v", table, refusal)
			}
			// DDL restoration changes the database-bound consent token, even though
			// every original row and reviewed impact must still match exactly.
			review, err := history.PreviewSmartlistRepair(t.Context(), filepath.Join(root, f.Name))
			if err != nil {
				t.Fatal(err)
			}
			impact := *review
			impact.PlanToken = f.Review.PlanToken
			if err := sameJSON(f.Review, &impact); err != nil {
				t.Fatalf("restored schema changed reviewed repair impact: %v", err)
			}
			f.Review.PlanToken = review.PlanToken
			if err := verify(); err != nil {
				t.Fatalf("restored projection rejected: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifyOne(ctx, root, g, f, "seed"); err == nil {
		t.Fatal("cancelled native verification accepted")
	}
	if err := verify(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeVerifierRequiresOriginalHistoryEvidence(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	root := t.TempDir()
	f, err := seedFixture(t.Context(), root, in, g, "reconnect")
	if err != nil {
		t.Fatal(err)
	}
	verify := func() error { return verifyOne(t.Context(), root, g, f, "seed") }
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, f.Name, ".engine-cli", "history", f.EntryID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{"missing", "changed"} {
		t.Run(damage, func(t *testing.T) {
			if damage == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			refusal := verify()
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if refusal == nil {
				t.Fatal("original History evidence loss accepted")
			}
			if err := verify(); err != nil {
				t.Fatalf("restored original History rejected: %v", err)
			}
		})
	}
}
