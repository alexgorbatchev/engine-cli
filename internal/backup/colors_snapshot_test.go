package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestColorSnapshotIncludesCommittedWAL(t *testing.T) {
	library := colorLibrary(t)
	setTestColor(t, library, "#112233")
	d, err := db.Open(companion.ColorsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	reader, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var color string
	if err := reader.QueryRow("SELECT color FROM TrackColor").Scan(&color); err != nil || color != "#112233" {
		t.Fatalf("reader=%q error=%v", color, err)
	}
	want := setTestColor(t, library, "#445566")
	info, err := CreateBackup(t.Context(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	setTestColor(t, library, "#778899")
	if err := RestoreBackup(t.Context(), info.Path, library); err != nil {
		t.Fatal(err)
	}
	got, err := colors.Read(t.Context(), library)
	if err != nil || got.Revision() != want.Revision() {
		t.Fatalf("committed WAL color lost: %+v error=%v", got, err)
	}
}

func TestHistoricalSnapshotRestoresColorAbsenceAndUndo(t *testing.T) {
	library := colorLibrary(t)
	info, err := CreateBackup(t.Context(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The historical format predates any companion snapshot section.
	if err := os.RemoveAll(filepath.Join(info.Path, companionFolder)); err != nil {
		t.Fatal(err)
	}
	want := setTestColor(t, library, "#123456")
	entry, err := history.Run(t.Context(), library, "library.restore", history.ScopeCollection, nil, func(ctx context.Context) error {
		return RestoreBackup(ctx, info.Path, library)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("historical restore retained future colors: %v", err)
	}
	if _, err := history.Undo(t.Context(), library, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := colors.Read(t.Context(), library)
	if err != nil || got.Revision() != want.Revision() {
		t.Fatalf("Undo lost replaced colors: %+v %v", got, err)
	}
}

func TestDamagedColorSnapshotRejectedBeforeLiveWrites(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt", "wrong schema", "false absence", "missing presence"} {
		t.Run(damage, func(t *testing.T) {
			library := colorLibrary(t)
			setTestColor(t, library, "#112233")
			info, err := CreateBackup(t.Context(), library, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			want := setTestColor(t, library, "#445566")
			engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if _, err := engine.Exec("UPDATE fixture SET value='newer Engine state'"); err != nil {
				t.Fatal(err)
			}
			artwork := filepath.Join(library, "Artwork", "sample_art.jpg")
			if err := os.WriteFile(artwork, []byte("newer artwork"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(info.Path, companionFolder, companion.ColorsName)
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "corrupt":
				err = os.WriteFile(path, []byte("invalid SQLite snapshot"), 0600)
			case "wrong schema":
				var snapshot *sql.DB
				snapshot, err = db.Open(path)
				if err == nil {
					_, err = snapshot.Exec("UPDATE Metadata SET version=99")
					if closeErr := snapshot.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
			case "false absence":
				err = os.WriteFile(filepath.Join(info.Path, companionFolder, companionManifest), []byte(`{"version":3,"tags":false,"imports":false,"colors":false}`), 0600)
			case "missing presence":
				err = os.WriteFile(filepath.Join(info.Path, companionFolder, companionManifest), []byte(`{"version":3,"tags":false,"imports":false}`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := RestoreBackup(t.Context(), info.Path, library); err == nil {
				t.Fatal("accepted damaged color snapshot")
			}
			got, err := colors.Read(t.Context(), library)
			if err != nil || got.Revision() != want.Revision() {
				t.Fatalf("rejection changed live colors: %+v error=%v", got, err)
			}
			var value string
			if err := engine.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "newer Engine state" {
				t.Fatalf("rejection changed Engine state: %q error=%v", value, err)
			}
			data, err := os.ReadFile(artwork)
			if err != nil || string(data) != "newer artwork" {
				t.Fatalf("rejection changed artwork: %q error=%v", data, err)
			}
		})
	}
}
