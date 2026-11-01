package fixturecapture

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestCaptureRefusesUnavailableOrOverlappingInputs(t *testing.T) {
	for _, scenario := range []string{"missing before", "missing after", "same input", "missing output parent"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			before, after, out := filepath.Join(base, "before"), filepath.Join(base, "after"), filepath.Join(base, "capture")
			for _, path := range []string{before, after} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("unchanged private evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "missing before":
				before = filepath.Join(base, "absent")
			case "missing after":
				after = filepath.Join(base, "absent")
			case "same input":
				after = before
			case "missing output parent":
				out = filepath.Join(base, "absent", "capture")
			}
			if _, err := Capture(t.Context(), before, after, out); err == nil {
				t.Fatal("unsafe capture accepted")
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal enrolled output: %v", err)
			}
			for _, name := range []string{"before", "after"} {
				data, err := os.ReadFile(filepath.Join(base, name, "notes.txt"))
				if err != nil || string(data) != "unchanged private evidence" {
					t.Fatalf("input changed: %q %v", data, err)
				}
			}
		})
	}
}

func TestSnapshotRefusesOccupiedWorkingOrDestination(t *testing.T) {
	for _, occupied := range []string{"working", "snapshots"} {
		t.Run(occupied, func(t *testing.T) {
			base := t.TempDir()
			archive := filepath.Join(base, "archive")
			if err := os.Mkdir(archive, 0o700); err != nil {
				t.Fatal(err)
			}
			database, err := sql.Open("sqlite", sqliteURI(filepath.Join(archive, "m.db"), "rwc"))
			if err != nil {
				t.Fatal(err)
			}
			_, execErr := database.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, Rules TEXT)")
			if err := errors.Join(execErr, database.Close()); err != nil {
				t.Fatal(err)
			}
			blocked := filepath.Join(base, occupied)
			if err := os.WriteFile(blocked, []byte("existing evidence"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := snapshotImage(t.Context(), archive, filepath.Join(base, "working"), filepath.Join(base, "snapshots")); err == nil {
				t.Fatal("occupied destination accepted")
			}
			data, err := os.ReadFile(blocked)
			if err != nil || string(data) != "existing evidence" {
				t.Fatalf("occupied evidence changed: %q %v", data, err)
			}
		})
	}
}

func TestSnapshotRefusesLogicalIntegrityViolation(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "m.db")
	database, err := sql.Open("sqlite", sqliteURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := database.Exec("CREATE TABLE evidence (value INTEGER CHECK(value > 0)); PRAGMA ignore_check_constraints=ON; INSERT INTO evidence VALUES (-1)")
	if err := errors.Join(execErr, database.Close()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "snapshot.db")
	if err := recoverSnapshot(t.Context(), path, destination); err == nil || !strings.Contains(err.Error(), "integrity check refused") {
		t.Fatalf("invalid logical image accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("refusal changed image: %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refusal published snapshot: %v", err)
	}
}

func TestExportRefusesMissingCorruptOrCancelledImage(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupt", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			path, output := filepath.Join(base, "m.db"), filepath.Join(base, "export.json")
			ctx := t.Context()
			if scenario != "missing" {
				if err := os.WriteFile(path, []byte("not a SQLite image"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := exportDatabase(ctx, path, output); err == nil {
				t.Fatal("unreadable image exported")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failure published export: %v", err)
			}
		})
	}
}

func TestRetainedCopyRefusesExpiredDescriptorOrUnwritableDestination(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "input")
	if err := os.WriteFile(path, []byte("private bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := copyFile(t.Context(), file, io.Discard); err == nil {
		t.Fatal("expired descriptor accepted")
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, file)
	readonly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, readonly)
	if _, err := copyFile(t.Context(), file, readonly); err == nil {
		t.Fatal("read-only destination accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private bytes" {
		t.Fatalf("input changed: %q %v", data, err)
	}
}

func TestExportRefusesVanishedTable(t *testing.T) {
	database, err := sql.Open("sqlite", sqliteURI(filepath.Join(t.TempDir(), "m.db"), "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, database)
	if _, err := database.Exec("CREATE TABLE vanished (value TEXT); DROP TABLE vanished"); err != nil {
		t.Fatal(err)
	}
	if err := exportTable(t.Context(), database, &Table{Name: "vanished"}); err == nil {
		t.Fatal("missing table accepted as empty export")
	}
}

func TestArchiveRefusesExpiredRootAndExistingArtifact(t *testing.T) {
	base := t.TempDir()
	source, destination := filepath.Join(base, "source"), filepath.Join(base, "archive")
	for _, path := range []string{source, destination} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{filepath.Join(source, "m.db"): "input evidence", filepath.Join(destination, "m.db"): "existing archive"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := watchfs.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := inventory(t.Context(), root, destination)
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(copyErr, os.ErrExist) {
		t.Fatalf("archive collision accepted: %v", copyErr)
	}
	if _, err := inventory(t.Context(), root, ""); err == nil {
		t.Fatal("expired root accepted")
	}
	data, err := os.ReadFile(filepath.Join(destination, "m.db"))
	if err != nil || string(data) != "existing archive" {
		t.Fatalf("archive overwritten: %q %v", data, err)
	}
	if err := snapshotImage(t.Context(), filepath.Join(base, "absent"), filepath.Join(base, "working"), filepath.Join(base, "snapshots")); err == nil {
		t.Fatal("missing archive accepted")
	}
}

func TestComparisonRefusesDamagedExportAndExistingReport(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(base, "snapshots", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "m.db.json"), []byte("{truncated export"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	image := Image{Files: []File{{Path: "m.db"}}}
	if err := compareImages(base, image, image); err == nil {
		t.Fatal("damaged export accepted")
	}
	report := filepath.Join(base, "row-changes.json")
	if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failure published report: %v", err)
	}
	if err := os.WriteFile(report, []byte("prior report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := compareImages(base, Image{}, Image{}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing report overwritten: %v", err)
	}
	data, err := os.ReadFile(report)
	if err != nil || string(data) != "prior report" {
		t.Fatalf("prior report changed: %q %v", data, err)
	}
}
