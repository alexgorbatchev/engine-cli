package fixturecapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestNativeContainmentRefusesLostOwnership(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing ancestor", true: "closed root"}[closed], func(t *testing.T) {
			base := t.TempDir()
			root, err := watchfs.Open(base)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(base, "absent")
			if closed {
				path = base
				if err := root.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Cleanup(func() {
					if err := root.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			if inside, err := nativeWithin(root, path); err == nil || inside {
				t.Fatalf("lost native ownership accepted: inside=%v error=%v", inside, err)
			}
		})
	}
}

func TestSnapshotRefusesIncompleteArchiveOrOccupiedExport(t *testing.T) {
	for _, scenario := range []string{"cancelled archive observation", "occupied export"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			archive := filepath.Join(base, "archive")
			captureNativeImage(t, archive)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled archive observation" {
				cancel()
			}
			before := observe(t, archive)
			snapshots := filepath.Join(base, "snapshots")
			if err := os.Mkdir(snapshots, 0o700); err != nil {
				t.Fatal(err)
			}
			export := filepath.Join(snapshots, "m.db.json")
			if scenario == "occupied export" {
				if err := os.WriteFile(export, []byte("prior private export"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := snapshotImage(ctx, archive, filepath.Join(base, "working"), snapshots); err == nil {
				t.Fatal("incomplete or occupied evidence accepted")
			}
			if !reflect.DeepEqual(before, observe(t, archive)) {
				t.Fatal("refusal changed archival input")
			}
			if scenario == "occupied export" {
				data, err := os.ReadFile(export)
				if err != nil || string(data) != "prior private export" {
					t.Fatalf("prior export changed: %q %v", data, err)
				}
			}
		})
	}
}

func TestComparisonRefusesDamagedAfterEvidence(t *testing.T) {
	base := t.TempDir()
	for name, data := range map[string]string{"before": "[]", "after": "{incomplete"} {
		dir := filepath.Join(base, "snapshots", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "m.db.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	image := Image{Files: []File{{Path: "m.db"}}}
	if err := compareImages(base, image, image); err == nil {
		t.Fatal("damaged after evidence accepted")
	}
	if _, err := os.Stat(filepath.Join(base, "row-changes.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refusal published comparison: %v", err)
	}
}
