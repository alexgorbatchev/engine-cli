package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCompanionSnapshotFailurePreservesOwnedPaths(t *testing.T) {
	for _, obstacle := range []string{"companion directory", "manifest", "database"} {
		t.Run(obstacle, func(t *testing.T) {
			library, staging := t.TempDir(), t.TempDir()
			if _, err := tags.EditCategory(t.Context(), library, tags.Edit{Name: "Mood", Revision: tagRevision(t, library)}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(companion.TagsPath(library))
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(staging, companionFolder)
			owned := dir
			if obstacle != "companion directory" {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if obstacle == "manifest" {
					owned = filepath.Join(dir, companionManifest)
					if err := os.Mkdir(owned, 0700); err != nil {
						t.Fatal(err)
					}
					owned = filepath.Join(owned, "marker")
				} else {
					owned = filepath.Join(dir, companion.TagsName)
				}
			}
			const marker = "previously owned content"
			if err := os.WriteFile(owned, []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := snapshotCompanion(t.Context(), library, staging); err == nil {
				t.Fatal("snapshot accepted an occupied destination")
			}
			got, err := os.ReadFile(owned)
			if err != nil || string(got) != marker {
				t.Fatalf("failed snapshot changed existing content: %q %v", got, err)
			}
			after, err := os.ReadFile(companion.TagsPath(library))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed snapshot modified source: %v", err)
			}
		})
	}
}

func TestCorruptRestoreSourceCannotReplaceCorruptDestination(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source.db"), filepath.Join(dir, "destination.db")
	contents := map[string]string{source: "damaged source", destination: "damaged destination"}
	for path, content := range contents {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreDatabase(t.Context(), source, destination); err == nil {
		t.Fatal("restored an invalid source over a damaged destination")
	}
	for path, content := range contents {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("failed repair changed %s: %q %v", filepath.Base(path), data, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(contents) {
		t.Fatalf("failed repair leaked staging files: %v %v", entries, err)
	}
}
