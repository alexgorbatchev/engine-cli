package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestRestoreRejectsUnreadableSnapshotDirectoriesBeforeWriting(t *testing.T) {
	for _, folder := range []string{"Database2", "Root", "Artwork"} {
		t.Run(folder, func(t *testing.T) {
			library := createDummyEngineLibrary(t)
			backup, err := CreateBackup(context.Background(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			broken := filepath.Join(backup.Path, folder)
			if err := os.RemoveAll(broken); err != nil {
				t.Fatal(err)
			}
			// A file at a directory path deterministically fails ReadDir on every OS.
			if err := os.WriteFile(broken, []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			live := filepath.Join(library, "Database2", "m.db")
			if err := os.WriteFile(live, []byte("keep current database"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreBackup(context.Background(), backup.Path, library); progress.ErrorCode(err) != "backup_unavailable" {
				t.Fatalf("restore did not report an unreadable backup: %v", err)
			}
			contents, err := os.ReadFile(live)
			if err != nil || string(contents) != "keep current database" {
				t.Fatalf("restore changed live data before validation: %q %v", contents, err)
			}
		})
	}
}

func TestRestoreRequiresDatabasesAndAllowsOptionalFolders(t *testing.T) {
	library := createDummyEngineLibrary(t)
	if err := RestoreBackup(context.Background(), t.TempDir(), library); progress.ErrorCode(err) != "backup_unavailable" {
		t.Fatalf("empty snapshot=%v", err)
	}
	backup, err := CreateBackup(context.Background(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"Root", "Artwork"} {
		if err := os.RemoveAll(filepath.Join(backup.Path, folder)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RestoreBackup(context.Background(), backup.Path, library); err != nil {
		t.Fatalf("optional folders=%v", err)
	}
}
