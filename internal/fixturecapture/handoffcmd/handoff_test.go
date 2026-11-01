package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func capturedGolden(t *testing.T) golden {
	t.Helper()
	data, err := os.ReadFile("../testdata/desktop-5.1.0/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if err := validateGolden(g); err != nil {
		t.Fatal(err)
	}
	return g
}

func nativeBinary(t *testing.T) inputs {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "engine-cli")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-p=1", "-o", binary, "../../../cmd/engine-cli")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build native CLI: %v\n%s", err, output)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return inputs{Binary: binding{Path: binary, SHA256: digest(data)}}
}

func TestNativeHandoffRepairRelocationAndUndo(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	original := filepath.Join(t.TempDir(), "original")
	if err := seed(t.Context(), original, in, g); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(original, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// No mutation is allowed until all four independent baselines are observed.
	for _, f := range m.Fixtures {
		if err := verifyOne(t.Context(), original, g, f, "seed"); err != nil {
			t.Fatal(err)
		}
	}
	repaired := make(map[string]*history.SmartlistRepairResult)
	for _, f := range m.Fixtures {
		lib := filepath.Join(original, f.Name)
		result, err := history.ApplySmartlistRepair(t.Context(), lib, f.Review.PlanToken)
		if err != nil {
			t.Fatal(err)
		}
		if result.Applied != (f.Name != "healthy") {
			t.Fatalf("wrong apply state: %+v", result)
		}
		repaired[f.Name] = result
		if err := verifyOne(t.Context(), original, g, f, "repaired"); err != nil {
			t.Fatal(err)
		}
		review, err := history.PreviewSmartlistRepair(t.Context(), lib)
		if err != nil {
			t.Fatal(err)
		}
		again, err := history.ApplySmartlistRepair(t.Context(), lib, review.PlanToken)
		if err != nil || again.Applied || again.UndoID != "" {
			t.Fatalf("not idempotent: %+v %v", again, err)
		}
	}
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(original, relocated); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old root remains: %v", err)
	}
	for _, f := range m.Fixtures {
		if err := verifyOne(t.Context(), relocated, g, f, "repaired"); err != nil {
			t.Fatalf("relocated %s: %v", f.Name, err)
		}
	}
	for _, f := range m.Fixtures {
		var id, state string
		switch f.Name {
		case "reconnect":
			id, state = repaired[f.Name].UndoID, "repair-undone"
		case "original-deletion":
			id, state = f.EntryID, "original-deletion-undone"
		default:
			continue
		}
		if id == "" {
			t.Fatal("missing exact Undo ID")
		}
		if _, err := history.Undo(t.Context(), filepath.Join(relocated, f.Name), id, history.UndoOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := verifyOne(t.Context(), relocated, g, f, state); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeHandoffRejectsMediaAndRulesDamage(t *testing.T) {
	g := capturedGolden(t)
	for _, damage := range []string{"missing", "changed", "absolute-old-root", "wrong-title", "wrong-duration", "rules", "healthy-edge"} {
		t.Run(damage, func(t *testing.T) {
			root := t.TempDir()
			lib := filepath.Join(root, "healthy")
			if err := os.Mkdir(lib, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := os.Mkdir(filepath.Join(lib, "Music"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, track := range g.Tracks {
				title, err := title(track)
				if err != nil {
					t.Fatal(err)
				}
				data, err := wave(track)
				if err != nil {
					t.Fatal(err)
				}
				if err := writeNew(filepath.Join(lib, "Music", audioName(track.Number)), data); err != nil {
					t.Fatal(err)
				}
				if _, err := d.ExecContext(t.Context(), `INSERT INTO Track(title,path,length,isAvailable,isAnalyzed) VALUES(?,?,1,1,0)`, title, filepath.ToSlash(filepath.Join("Music", audioName(track.Number)))); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyMedia(t.Context(), lib, d, g); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lib, "Music", audioName(1))
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "changed":
				err = os.WriteFile(path, []byte("not audio"), 0600)
			case "absolute-old-root":
				_, err = d.ExecContext(t.Context(), `UPDATE Track SET path=? WHERE id=1`, filepath.Join(t.TempDir(), "unavailable", "fixture-01.wav"))
			case "wrong-title":
				_, err = d.ExecContext(t.Context(), `UPDATE Track SET title='wrong' WHERE id=1`)
			case "wrong-duration":
				_, err = d.ExecContext(t.Context(), `UPDATE Track SET length=2 WHERE id=1`)
			case "rules", "healthy-edge":
				_, err = d.ExecContext(t.Context(), `INSERT INTO Smartlist(listUuid,title,rules) VALUES(?,?,?)`, healthyID, "T3 healthy Techno", rules(g, "T3"))
				if err != nil {
					t.Fatal(err)
				}
				before, err := snapshot(t.Context(), d)
				if err != nil {
					t.Fatal(err)
				}
				review, err := history.PreviewSmartlistRepair(t.Context(), lib)
				if err != nil {
					t.Fatal(err)
				}
				f := fixture{Name: "healthy", Original: before, Review: review}
				if err := d.QueryRowContext(t.Context(), "SELECT uuid FROM Information").Scan(&f.UUID); err != nil {
					t.Fatal(err)
				}
				if err := verifyOne(t.Context(), root, g, f, "seed"); err != nil {
					t.Fatal(err)
				}
				statement := `UPDATE Smartlist SET rules='{}'`
				if damage == "healthy-edge" {
					statement = `UPDATE Smartlist SET nextListUuid='unexpected-sibling'`
				}
				if _, err = d.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
				if err := verifyOne(t.Context(), root, g, f, "seed"); err == nil || !strings.Contains(err.Error(), "complete native rows changed") {
					t.Fatalf("full-row verifier did not identify changed %s: %v", damage, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if verifyMedia(t.Context(), lib, d, g) == nil {
				t.Fatalf("accepted %s", damage)
			}
		})
	}
}

func TestHandoffNoClobberAndOpaqueGoldenRefusal(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "existing")
	if err := os.WriteFile(sentinel, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seed(context.Background(), root, inputs{}, capturedGolden(t)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("occupied root: %v", err)
	}
	if err := writeNew(sentinel, []byte("overwritten")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("occupied file: %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "retained" {
		t.Fatalf("clobbered: %s %v", data, err)
	}
	g := capturedGolden(t)
	for i := range g.Cases {
		if g.Cases[i].Name == "T1" {
			g.Cases[i].Rules = "{}"
			g.Cases[i].SHA256 = digest([]byte("{}"))
		}
	}
	if validateGolden(g) == nil {
		t.Fatal("valid JSON with substituted opaque Rules accepted")
	}
}
