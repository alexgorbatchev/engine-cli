package history

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func smartlistRestrict(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("native Unix permission refusal requires an unprivileged process")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, info.Mode().Perm()); err != nil {
			t.Error(err)
		}
	})
}

func TestSmartlistIntentPermissionRefusalsPreserveEvidence(t *testing.T) {
	for _, kind := range []string{"read-only image", "unreadable pending", "inaccessible directory"} {
		t.Run(kind, func(t *testing.T) {
			dir, journal := smartlistJournalFixture(t)
			ctx := context.Background()
			original := filepath.Join(dir, journal.Images[0].Original)
			before, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "read-only image":
				smartlistRestrict(t, original, 0400)
				if err := prepareSmartlistJournal(ctx, dir, journal); !os.IsPermission(err) {
					t.Fatalf("preparation did not reject unwritable evidence: %v", err)
				}
				if _, err := os.Lstat(filepath.Join(dir, smartlistJournalName)); !os.IsNotExist(err) {
					t.Fatalf("refused preparation published intent: %v", err)
				}
			case "unreadable pending":
				if err := prepareSmartlistJournal(ctx, dir, journal); err != nil {
					t.Fatal(err)
				}
				pending := filepath.Join(dir, smartlistJournalName)
				smartlistRestrict(t, pending, 0)
				if got, err := readSmartlistJournal(ctx, dir); got != nil || !os.IsPermission(err) {
					t.Fatalf("unreadable intent accepted: %+v %v", got, err)
				}
				if _, err := os.Lstat(pending); err != nil {
					t.Fatalf("refused read removed pending evidence: %v", err)
				}
			case "inaccessible directory":
				smartlistRestrict(t, dir, 0)
				if err := prepareSmartlistJournal(ctx, dir, journal); !os.IsPermission(err) {
					t.Fatalf("inaccessible evidence accepted: %v", err)
				}
				if err := os.Chmod(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			after, err := os.ReadFile(original)
			if err != nil || string(after) != string(before) {
				t.Fatalf("refusal changed rollback evidence: %q %v", after, err)
			}
		})
	}
}

func TestSmartlistDirectoryEnrollmentRefusesUnsafeParents(t *testing.T) {
	for _, kind := range []string{"missing parent", "file parent", "symlink parent", "unwritable parent", "inaccessible parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "parent")
			switch kind {
			case "file parent":
				if err := os.WriteFile(parent, []byte("external"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink parent":
				if err := os.Symlink(t.TempDir(), parent); err != nil {
					t.Skipf("native symlink unavailable: %v", err)
				}
			case "unwritable parent", "inaccessible parent":
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0500)
				if kind == "inaccessible parent" {
					mode = 0
				}
				smartlistRestrict(t, parent, mode)
			}
			path := filepath.Join(parent, "repair")
			if err := createSmartlistDirectory(path); err == nil {
				t.Fatal("unsafe parent enrolled repair storage")
			}
			if kind == "inaccessible parent" {
				if err := os.Chmod(parent, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Lstat(path); err == nil {
				t.Fatal("failed enrollment created repair directory")
			}
		})
	}
}

func TestSmartlistReferencedImagePublicationFailure(t *testing.T) {
	for _, kind := range []string{"read-only image", "blocked readiness marker"} {
		t.Run(kind, func(t *testing.T) {
			lib, dir, _ := smartlistTargetFixture(t)
			imageDir := filepath.Join(lib, ".engine-cli", "smartlist-repairs", "images")
			if err := os.MkdirAll(imageDir, 0700); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(imageDir, "original.json")
			const content = "immutable reference"
			if err := os.WriteFile(image, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "read-only image" {
				smartlistRestrict(t, image, 0400)
			} else if err := os.Mkdir(filepath.Join(imageDir, ".smartlist-images-ready"), 0700); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(lib, image)
			if err != nil {
				t.Fatal(err)
			}
			if err := syncSmartlistReferences(context.Background(), dir, []smartlistJournalReference{{Path: rel, Hash: hashText(content)}}); err == nil {
				t.Fatal("failed reference publication accepted")
			}
			data, err := os.ReadFile(image)
			if err != nil || string(data) != content {
				t.Fatalf("reference changed: %q %v", data, err)
			}
		})
	}
}
