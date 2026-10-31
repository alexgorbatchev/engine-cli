package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestRestoreIndependentColorsAndImportEvidence(t *testing.T) {
	for _, version := range []string{"current", "released imports-only format"} {
		t.Run(version, func(t *testing.T) {
			library := colorLibrary(t)
			if _, err := tags.EditCategory(t.Context(), library, tags.Edit{Name: "Mood", Revision: tagRevision(t, library)}); err != nil {
				t.Fatal(err)
			}
			archive := publishArchive(t, library, "Original")
			if version == "current" {
				setTestColor(t, library, "#112233")
			}
			before, err := colors.Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := CreateBackup(t.Context(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if version != "current" {
				if err := os.WriteFile(filepath.Join(backup.Path, companionFolder, companionManifest), []byte(`{"version":2,"tags":true,"imports":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			later := setTestColor(t, library, "#445566")
			laterArchive := publishArchive(t, library, "Later")
			entry, err := history.Run(t.Context(), library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
				return RestoreBackup(ctx, backup.Path, library)
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := colors.Read(t.Context(), library)
			if err != nil || got.Revision() != before.Revision() || archiveState(t, library).Revision() != archive.Revision() {
				t.Fatalf("restore mixed independent states: colors=%+v error=%v", got, err)
			}
			listed, err := tags.List(t.Context(), library)
			if err != nil || len(listed.Categories) != 1 || listed.Categories[0].Name != "Mood" {
				t.Fatalf("restore lost taxonomy: %+v error=%v", listed, err)
			}
			if version != "current" {
				if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
					t.Fatalf("historical backup did not restore color absence: %v", err)
				}
			}
			if _, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			got, err = colors.Read(t.Context(), library)
			if err != nil || got.Revision() != later.Revision() || archiveState(t, library).Revision() != laterArchive.Revision() {
				t.Fatalf("Undo lost independent later states: colors=%+v error=%v", got, err)
			}
		})
	}
}
