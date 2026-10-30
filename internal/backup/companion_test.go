package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func tagRevision(t *testing.T, library string) string {
	t.Helper()
	s, err := tags.Read(context.Background(), library)
	if err != nil {
		t.Fatal(err)
	}
	return s.Revision()
}

func TestBackupIncludesCommittedCompanionWAL(t *testing.T) {
	ctx := context.Background()
	lib := createDummyEngineLibrary(t)
	c, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: tagRevision(t, lib)})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(companion.TagsPath(lib))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	reader, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var name string
	if err := reader.QueryRow("SELECT name FROM Category").Scan(&name); err != nil {
		t.Fatal(err)
	}
	updated, err := tags.EditCategory(ctx, lib, tags.Edit{ID: c.ID, Name: "Energy", Revision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	info, err := CreateBackup(ctx, lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.DBFiles != 4 || info.ArtFiles != 1 {
		t.Fatalf("snapshot counts: %+v", info)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := tags.EditCategory(ctx, lib, tags.Edit{ID: c.ID, Name: "Later", Revision: updated.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(ctx, info.Path, lib); err != nil {
		t.Fatal(err)
	}
	listed, err := tags.List(ctx, lib)
	if err != nil || listed.Categories[0].Name != "Energy" {
		t.Fatalf("WAL snapshot lost committed name: %+v %v", listed, err)
	}
}

func TestRestoreOldAndExplicitAbsentSnapshotsRemovesFutureCompanion(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit absence", true: "old snapshot"}[old], func(t *testing.T) {
			ctx := context.Background()
			lib := createDummyEngineLibrary(t)
			info, err := CreateBackup(ctx, lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if old {
				if err := os.RemoveAll(filepath.Join(info.Path, companionFolder)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Future", Revision: tagRevision(t, lib)}); err != nil {
				t.Fatal(err)
			}
			entry, err := history.Run(ctx, lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error { return RestoreBackup(ctx, info.Path, lib) })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.TagsPath(lib)); !os.IsNotExist(err) {
				t.Fatalf("future companion survived old restore: %v", err)
			}
			if _, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			listed, err := tags.List(ctx, lib)
			if err != nil || listed.Categories[0].Name != "Future" {
				t.Fatalf("restore undo lost future tags: %+v %v", listed, err)
			}
		})
	}
}

func TestCompanionPresenceDamageRejectedBeforeEngineWrite(t *testing.T) {
	for _, damage := range []string{"missing manifest", "missing database", "missing presence field", "wrong schema"} {
		t.Run(damage, func(t *testing.T) {
			ctx := context.Background()
			lib := createDummyEngineLibrary(t)
			if _, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: tagRevision(t, lib)}); err != nil {
				t.Fatal(err)
			}
			info, err := CreateBackup(ctx, lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(info.Path, companionFolder, companionManifest)
			path := filepath.Join(info.Path, companionFolder, companion.TagsName)
			switch damage {
			case "missing manifest":
				err = os.Remove(manifest)
			case "missing database":
				err = os.Remove(path)
			case "missing presence field":
				err = os.WriteFile(manifest, []byte(`{"version":1}`), 0600)
			case "wrong schema":
				var dErr error
				d, dErr := db.Open(path)
				if dErr != nil {
					t.Fatal(dErr)
				}
				_, err = d.Exec("UPDATE Metadata SET version=99")
				if closeErr := d.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lib, "Artwork", "sample_art.jpg"), []byte("live newer artwork"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreBackup(ctx, info.Path, lib); err == nil {
				t.Fatal("accepted damaged companion snapshot")
			}
			data, err := os.ReadFile(filepath.Join(lib, "Artwork", "sample_art.jpg"))
			if err != nil || string(data) != "live newer artwork" {
				t.Fatalf("restore touched live content before rejecting: %s %v", data, err)
			}
		})
	}
}
