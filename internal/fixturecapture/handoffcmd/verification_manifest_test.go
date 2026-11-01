package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestVerificationManifestRequiresReadableMatchingSeedEvidence(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	root := filepath.Join(t.TempDir(), "fixtures")
	if err := seed(t.Context(), root, in, g); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "manifest.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t.Context(), root, in, g, "", "seed"); err != nil {
		t.Fatalf("intact four-case baseline: %v", err)
	}
	for _, damage := range []string{"missing", "malformed", "source-commit", "golden-binding"} {
		t.Run(damage, func(t *testing.T) {
			t.Cleanup(func() {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Errorf("restore manifest: %v", err)
				}
			})
			var m manifest
			if err := json.Unmarshal(original, &m); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte("{broken manifest"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if damage == "source-commit" {
					m.Inputs.SourceCommit += "-different-source"
				} else {
					m.Inputs.Golden.Path += "-different-capture"
				}
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			refusal := verify(t.Context(), root, in, g, "", "seed")
			var syntax *json.SyntaxError
			switch damage {
			case "missing":
				if !errors.Is(refusal, os.ErrNotExist) {
					t.Fatalf("missing manifest refusal: %v", refusal)
				}
			case "malformed":
				if !errors.As(refusal, &syntax) {
					t.Fatalf("malformed manifest refusal: %v", refusal)
				}
			default:
				if refusal == nil || !strings.Contains(refusal.Error(), "seed provenance changed") {
					t.Fatalf("mismatched seed provenance accepted: %v", refusal)
				}
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verify(t.Context(), root, in, g, "", "seed"); err != nil {
				t.Fatalf("restored four-case baseline: %v", err)
			}
		})
	}

	var m manifest
	if err := json.Unmarshal(original, &m); err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Fixtures {
		if f.Name != "reconnect" {
			continue
		}
		t.Run("invalid-native-selection", func(t *testing.T) {
			invalid := f
			invalid.Name = "../outside"
			if err := verifyOne(t.Context(), root, g, invalid, "seed"); err == nil || !strings.Contains(err.Error(), "invalid fixture name") {
				t.Fatalf("outside fixture accepted: %v", err)
			}
			lib := filepath.Join(root, f.Name)
			withheld := filepath.Join(root, "withheld-reconnect")
			if err := os.Rename(lib, withheld); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := os.Stat(withheld); err == nil {
					if err := os.Rename(withheld, lib); err != nil {
						t.Errorf("restore native library: %v", err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("inspect withheld native library: %v", err)
				}
			})
			refusal := verifyOne(t.Context(), root, g, f, "seed")
			var cause *sqlite.Error
			if !errors.As(refusal, &cause) || cause.Code() != sqlite3.SQLITE_CANTOPEN {
				t.Fatalf("missing native library refusal: %v", refusal)
			}
			if _, err := os.Stat(lib); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("read-only verification recreated missing library: %v", err)
			}
			if err := os.Rename(withheld, lib); err != nil {
				t.Fatal(err)
			}
			if err := verifyOne(t.Context(), root, g, f, "seed"); err != nil {
				t.Fatalf("restored native selection: %v", err)
			}
		})
		t.Run("repair-state-evidence", func(t *testing.T) {
			lib := filepath.Join(root, f.Name)
			result, err := history.ApplySmartlistRepair(t.Context(), lib, f.Review.PlanToken)
			if err != nil || !result.Applied || result.UndoID == "" {
				t.Fatalf("native reconnect repair: %+v %v", result, err)
			}
			if err := verifyOne(t.Context(), root, g, f, "repaired"); err != nil {
				t.Fatalf("actual repaired rows: %v", err)
			}
			var damaged fixture
			data, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &damaged); err != nil {
				t.Fatal(err)
			}
			image := damaged.Original["Smartlist"]
			image.Columns = []string{"unrelated-column"}
			damaged.Original["Smartlist"] = image
			if err := verifyOne(t.Context(), root, g, damaged, "repaired"); err == nil || !strings.Contains(err.Error(), "missing saved-list identity/order columns") {
				t.Fatalf("uninterpretable recorded repair image accepted: %v", err)
			}
			if _, err := history.Undo(t.Context(), lib, result.UndoID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := verifyOne(t.Context(), root, g, f, "repair-undone"); err != nil {
				t.Fatalf("exact-ID Undo baseline: %v", err)
			}
			damaged = f
			review := *f.Review
			review.Reconnected++
			damaged.Review = &review
			if err := verifyOne(t.Context(), root, g, damaged, "repair-undone"); err == nil || !strings.Contains(err.Error(), "repair Undo changed the original repairable component") {
				t.Fatalf("tampered Undo repair impact accepted: %v", err)
			}
			if err := verifyOne(t.Context(), root, g, f, "repair-undone"); err != nil {
				t.Fatalf("unmodified Undo evidence: %v", err)
			}
		})
	}
}
