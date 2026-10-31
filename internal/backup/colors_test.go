package backup

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func colorLibrary(t *testing.T) string {
	t.Helper()
	library := createDummyEngineLibrary(t)
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`CREATE TABLE Track(id INTEGER PRIMARY KEY,originDatabaseUuid TEXT,originTrackId INTEGER);
INSERT INTO Track VALUES(1,'fixture',1)`); err != nil {
		t.Fatal(err)
	}
	return library
}

func setTestColor(t *testing.T, library, color string) colors.State {
	t.Helper()
	ctx := context.Background()
	state, err := colors.Read(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	next := colors.State{Assignments: []colors.Assignment{{OriginUUID: "fixture", OriginTrackID: 1, Color: color}}}
	result, err := colors.Replace(ctx, library, state.Revision(), next)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIndependentColorBackupRestoreAndUndo(t *testing.T) {
	for _, withTags := range []bool{false, true} {
		t.Run(map[bool]string{false: "colors only", true: "both companions"}[withTags], func(t *testing.T) {
			ctx := context.Background()
			lib := colorLibrary(t)
			if withTags {
				if _, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: tagRevision(t, lib)}); err != nil {
					t.Fatal(err)
				}
			}
			original := setTestColor(t, lib, "#12ABEF")
			info, err := CreateBackup(ctx, lib, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			later := setTestColor(t, lib, "#FF0000")
			entry, err := history.Run(ctx, lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, info.Path, lib)
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := colors.Read(ctx, lib)
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Fatalf("restored colors: %+v %v", got, err)
			}
			if !withTags {
				if _, err := os.Stat(companion.TagsPath(lib)); !os.IsNotExist(err) {
					t.Fatalf("color restore enrolled tags: %v", err)
				}
			}
			if _, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			got, err = colors.Read(ctx, lib)
			if err != nil || !reflect.DeepEqual(got, later) {
				t.Fatalf("undo colors: %+v %v", got, err)
			}
		})
	}
}

func TestTagOnlyBackupRestoresColorAbsenceAndUndo(t *testing.T) {
	ctx := context.Background()
	lib := colorLibrary(t)
	if _, err := tags.EditCategory(ctx, lib, tags.Edit{Name: "Mood", Revision: tagRevision(t, lib)}); err != nil {
		t.Fatal(err)
	}
	info, err := CreateBackup(ctx, lib, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// This is the released, tags-only backup format, not a migrated database.
	manifest := filepath.Join(info.Path, companionFolder, companionManifest)
	if err := os.WriteFile(manifest, []byte(`{"version":1,"tags":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	later := setTestColor(t, lib, "#00FF00")
	entry, err := history.Run(ctx, lib, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
		return RestoreBackup(ctx, info.Path, lib)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(companion.ColorsPath(lib)); !os.IsNotExist(err) {
		t.Fatalf("future colors survived old backup: %v", err)
	}
	if _, err := history.Undo(ctx, lib, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := colors.Read(ctx, lib)
	if err != nil || !reflect.DeepEqual(got, later) {
		t.Fatalf("undo future colors: %+v %v", got, err)
	}
}
