package backup

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func createSnapshotFixture(t *testing.T, path, value string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	database, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES(?)", value); err != nil {
		t.Fatal(err)
	}
}

func TestBackupFailurePublishesNothing(t *testing.T) {
	for _, scenario := range []string{"invalid database", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			library := createDummyEngineLibrary(t)
			out := t.TempDir()
			ctx := context.Background()
			if scenario == "invalid database" {
				if err := os.WriteFile(filepath.Join(library, "broken.db"), []byte("invalid sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := CreateBackup(ctx, library, out); err == nil {
				t.Fatal("expected backup failure")
			}
			backups, err := ListBackups("", out)
			if err != nil || len(backups) != 0 {
				t.Fatalf("failed backup visible: %+v error=%v", backups, err)
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed backup left staging files: %+v %v", entries, err)
			}
		})
	}
}

func TestBackupRepeatedCreationPreservesPriorSnapshot(t *testing.T) {
	library := createDummyEngineLibrary(t)
	out := t.TempDir()
	first, err := CreateBackup(context.Background(), library, out)
	if err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("UPDATE fixture SET value='second snapshot'"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := CreateBackup(context.Background(), library, out)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path {
		t.Fatal("second backup overwrote first snapshot")
	}
	for _, tt := range []struct{ path, want string }{{first.Path, "sample m.db content"}, {second.Path, "second snapshot"}} {
		database, err := sql.Open("sqlite", filepath.Join(tt.path, "Database2", "m.db"))
		if err != nil {
			t.Fatal(err)
		}
		var got string
		err = database.QueryRow("SELECT value FROM fixture").Scan(&got)
		if closeErr := database.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil || got != tt.want {
			t.Fatalf("snapshot=%q want=%q error=%v", got, tt.want, err)
		}
	}
}

func TestSnapshotDatabaseSpecialPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source ?#% ü.db")
	target := filepath.Join(root, "snapshot ?#% ü.db")
	createSnapshotFixture(t, source, "preserved URI characters")
	size, err := snapshotDatabase(context.Background(), source, target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || size != info.Size() || size == 0 {
		t.Fatalf("snapshot size=%d stat=%v error=%v", size, info, err)
	}
	u := url.URL{Scheme: "file", Path: target}
	database, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value string
	if err := database.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "preserved URI characters" {
		t.Fatalf("snapshot value=%q error=%v", value, err)
	}
}

func TestSnapshotDatabaseFailures(t *testing.T) {
	for _, scenario := range []string{"missing source", "directory source", "invalid database", "canceled", "missing destination directory", "directory destination"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source.db")
			target := filepath.Join(root, "snapshot.db")
			ctx := context.Background()
			switch scenario {
			case "missing source":
			case "directory source":
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
			case "invalid database":
				if err := os.WriteFile(source, []byte("not sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				createSnapshotFixture(t, source, "source retained")
			}
			if scenario == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "missing destination directory" {
				target = filepath.Join(root, "missing", "snapshot.db")
			}
			if scenario == "directory destination" {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := snapshotDatabase(ctx, source, target); err == nil {
				t.Fatal("expected snapshot error")
			}
			if scenario != "directory destination" {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("failed snapshot remains: %v", err)
				}
			}
			if scenario == "missing source" {
				if _, err := os.Stat(source); !os.IsNotExist(err) {
					t.Fatalf("missing source created: %v", err)
				}
			}
		})
	}
}

func TestSnapshotCannotOverwriteExistingDatabase(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	target := filepath.Join(root, "snapshot.db")
	createSnapshotFixture(t, source, "source")
	createSnapshotFixture(t, target, "previous snapshot")
	if _, err := snapshotDatabase(context.Background(), source, target); err == nil {
		t.Fatal("existing snapshot overwritten")
	}
	database, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var got string
	if err := database.QueryRow("SELECT value FROM fixture").Scan(&got); err != nil || got != "previous snapshot" {
		t.Fatalf("existing snapshot changed: %q %v", got, err)
	}
}

func TestBackupCannotResolveDefaultDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	if _, err := GetDefaultBackupDir(""); err == nil {
		t.Fatal("expected missing home error")
	}
	if _, err := CreateBackup(context.Background(), "", ""); err == nil {
		t.Fatal("expected default backup directory error")
	}
	if _, err := ListBackups("", ""); err == nil {
		t.Fatal("expected default backup listing error")
	}
}

func TestBackupOutputAndRestoreWriteErrors(t *testing.T) {
	library := createDummyEngineLibrary(t)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateBackup(context.Background(), library, blocked); err == nil {
		t.Fatal("accepted output file as directory")
	}
	if _, err := ListBackups("", blocked); err == nil {
		t.Fatal("listed file as backup directory")
	}
	info, err := CreateBackup(context.Background(), library, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := os.MkdirAll(filepath.Join(destination, "Database2", "m.db"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(context.Background(), info.Path, destination); err == nil {
		t.Fatal("restored database over directory")
	}
	if _, err := copyFile(filepath.Join(library, "missing"), filepath.Join(destination, "missing")); err == nil {
		t.Fatal("copied missing file")
	}
	if _, err := copyFile(library, filepath.Join(destination, "directory-copy")); err == nil {
		t.Fatal("copied directory as file")
	}
}

func TestBackupUnreadableSourceLeavesNoSnapshot(t *testing.T) {
	for _, scenario := range []string{"database directory is file", "broken artwork link"} {
		t.Run(scenario, func(t *testing.T) {
			library := t.TempDir()
			out := t.TempDir()
			if scenario == "database directory is file" {
				if err := os.WriteFile(filepath.Join(library, "Database2"), []byte("invalid directory"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				createSnapshotFixture(t, filepath.Join(library, "m.db"), "retained")
				art := filepath.Join(library, "Artwork")
				if err := os.Mkdir(art, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(library, "missing.jpg"), filepath.Join(art, "broken.jpg")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CreateBackup(context.Background(), library, out); err == nil {
				t.Fatal("unreadable source accepted")
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 0 {
				t.Fatalf("incomplete backup remains: %+v %v", entries, err)
			}
		})
	}
}
