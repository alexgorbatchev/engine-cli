package tags

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestIndependentImportArchiveDoesNotEnrollOrReplaceTags(t *testing.T) {
	library := t.TempDir()
	path := companion.ImportsPath(library)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	archive := []byte("independent archive contents")
	if err := os.WriteFile(path, archive, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Read(t.Context(), library)
	if err != nil || len(s.Categories) != 0 {
		t.Fatalf("empty tags=%+v error=%v", s, err)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read enrolled tags: %v", err)
	}
	if _, err := EditCategory(t.Context(), library, Edit{Name: "Mood", Revision: s.Revision()}); err != nil {
		t.Fatal(err)
	}
	s, err = Read(t.Context(), library)
	if err != nil || len(s.Categories) != 1 || s.Categories[0].Name != "Mood" {
		t.Fatalf("stored tags=%+v error=%v", s, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(archive) {
		t.Fatalf("tag write changed import archive: %q error=%v", got, err)
	}
}

func TestCompanionStorageFailuresHaveStableCodes(t *testing.T) {
	for _, name := range []string{"corrupt", "unsupported schema", "symlink"} {
		t.Run(name, func(t *testing.T) {
			library := t.TempDir()
			path := companion.TagsPath(library)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "corrupt":
				if err := os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsupported schema":
				d, err := db.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := d.Exec("CREATE TABLE Metadata(id INTEGER PRIMARY KEY,version INTEGER); INSERT INTO Metadata VALUES(1,99)"); err != nil {
					t.Fatal(err)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Fatal(err)
				}
			}
			_, err := List(t.Context(), library)
			requireCode(t, err, "tag_store_unavailable")
		})
	}
}

func TestCancelledReadDoesNotEnrollCompanion(t *testing.T) {
	library := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Read(ctx, library)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled read enrolled companion: %v", err)
	}
}
