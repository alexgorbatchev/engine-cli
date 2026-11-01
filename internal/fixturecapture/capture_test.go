package fixturecapture

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestCaptureOfflineJournalFamilies(t *testing.T) {
	for _, mode := range []string{"wal", "journal"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			before := filepath.Join(base, "before")
			after := filepath.Join(base, "after")
			for _, path := range []string{before, after} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOfflineWriterChild$")
				cmd.Env = append(os.Environ(), "FIXTURE_CAPTURE_CHILD="+filepath.Join(path, "m.db"), "FIXTURE_CAPTURE_MODE="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("child: %v %s", err, out)
				}
				if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("synthetic private evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			original := observe(t, before)
			if mode == "wal" {
				if _, err := os.Stat(filepath.Join(before, "m.db-wal")); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := os.Stat(filepath.Join(before, "m.db-journal")); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(base, "capture")
			report, err := Capture(t.Context(), before, after, output)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, observe(t, before)) {
				t.Fatal("original family changed")
			}
			if !reflect.DeepEqual(original, report.Before) {
				t.Fatal("archive inventory differs")
			}
			archive := filepath.Join(output, "archive", "before")
			for _, record := range original.Files {
				want, err := os.ReadFile(filepath.Join(before, record.Path))
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(archive, record.Path))
				if err != nil {
					t.Fatal(err)
				}
				if string(want) != string(got) {
					t.Fatalf("archive bytes changed: %s", record.Path)
				}
			}
			db, err := sql.Open("sqlite", sqliteURI(filepath.Join(output, "snapshots", "before", "m.db"), "ro"))
			if err != nil {
				t.Fatal(err)
			}
			defer closeTest(t, db)
			var count int
			var rules string
			if err := db.QueryRow("SELECT count(*), Rules FROM Smartlist").Scan(&count, &rules); err != nil {
				t.Fatal(err)
			}
			if count != 1 || rules != "literal \x00 rules "+string([]byte{255}) {
				t.Fatalf("lost committed raw rules: %d %q", count, rules)
			}
			var prefix string
			if err := db.QueryRow("SELECT hex(substr(v,1,8)) FROM Payload").Scan(&prefix); err != nil {
				t.Fatal(err)
			}
			if prefix != "0000000000000000" {
				t.Fatalf("uncommitted spilled payload survived recovery: %s", prefix)
			}
			var tables []Table
			data, err := os.ReadFile(filepath.Join(output, "snapshots", "before", "m.db.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &tables); err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, table := range tables {
				if table.Name == "Smartlist" {
					found = true
					if len(table.Rows) != 1 || table.Rows[0][1].Type != "text" || table.Rows[0][1].Data != base64.StdEncoding.EncodeToString([]byte(rules)) || table.Rows[0][2].Type != "null" {
						t.Fatalf("raw SQL values lost: %#v", table.Rows)
					}
				}
			}
			if !found {
				t.Fatal("Smartlist not exported")
			}
			if _, err := Capture(t.Context(), before, after, output); !errors.Is(err, os.ErrExist) {
				t.Fatalf("clobber accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(output, "complete.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOfflineWriterChild(t *testing.T) {
	path := os.Getenv("FIXTURE_CAPTURE_CHILD")
	if path == "" {
		return
	}
	db, err := sql.Open("sqlite", sqliteURI(path, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	mode := os.Getenv("FIXTURE_CAPTURE_MODE")
	if mode == "wal" {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, Rules TEXT, ParentPath TEXT); CREATE TABLE Payload (v BLOB); INSERT INTO Payload VALUES (zeroblob(2000000))"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO Smartlist VALUES ('synthetic', ?, NULL)", "literal \x00 rules "+string([]byte{255})); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA cache_size=10; BEGIN IMMEDIATE; UPDATE Payload SET v=randomblob(2000000); INSERT INTO Smartlist VALUES ('uncommitted', '{}', '')"); err != nil {
		t.Fatal(err)
	}
	// A real process exit leaves SQLite's actual journal/WAL family. No journal
	// headers or recovery markers are fabricated by this test.
	os.Exit(0)
}

func observe(t *testing.T, path string) Image {
	t.Helper()
	dir, err := watchfs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, dir)
	image, err := inventory(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func TestCaptureRefusesUnsafeOrIncompleteInputs(t *testing.T) {
	for _, kind := range []string{"link", "unknown-sidecar", "orphan", "missing", "corrupt", "cancel", "nested-output"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			a := filepath.Join(base, "a")
			b := filepath.Join(base, "b")
			out := filepath.Join(base, "out")
			for _, path := range []string{a, b} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "m.db"), []byte("not SQLite"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			switch kind {
			case "link":
				if err := os.Symlink(filepath.Join(b, "m.db"), filepath.Join(a, "linked.db")); err != nil {
					t.Fatal(err)
				}
			case "unknown-sidecar":
				if err := os.WriteFile(filepath.Join(a, "m.db-mystery"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "orphan":
				if err := os.WriteFile(filepath.Join(a, "other.db-journal"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				a = filepath.Join(base, "absent")
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nested-output":
				out = filepath.Join(a, "out")
			}
			if _, err := Capture(ctx, a, b, out); err == nil {
				t.Fatal("unsafe capture succeeded")
			}
			if _, err := os.Stat(filepath.Join(out, "complete.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failure published completion: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(b, "m.db")); err != nil || string(got) != "not SQLite" {
				t.Fatalf("input changed: %q %v", got, err)
			}
		})
	}
}

type changeWriter struct{ write func([]byte) (int, error) }

func (w changeWriter) Write(p []byte) (int, error) { return w.write(p) }

func TestRetainedCopyDetectsChangeAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "source change", true: "cancellation"}[cancelled], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte(strings.Repeat("a", 200000)), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer closeTest(t, file)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writer := changeWriter{write: func(p []byte) (int, error) {
				if cancelled {
					cancel()
				} else {
					if err := os.Truncate(path, 1); err != nil {
						return 0, err
					}
				}
				return len(p), nil
			}}
			_, err = copyFile(ctx, file, writer)
			if err == nil {
				t.Fatal("unstable copy accepted")
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func closeTest(t *testing.T, value io.Closer) {
	t.Helper()
	if err := value.Close(); err != nil {
		t.Error(err)
	}
}

func TestFullInputVerificationRejectsChangedTree(t *testing.T) {
	base := t.TempDir()
	a, err := watchfs.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, a)
	image, err := inventory(t.Context(), a, "")
	if err != nil {
		t.Fatal(err)
	}
	report := &Report{Before: image, After: image}
	if err := os.WriteFile(filepath.Join(base, "arrived.db-wal"), []byte("new sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyImages(t.Context(), a, a, report); err == nil {
		t.Fatal("new source family entry was accepted")
	}
	if err := os.Rename(base, base+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(base+"-moved", base); err != nil {
			t.Error(err)
		}
	})
	if _, err := inventory(t.Context(), a, ""); err == nil {
		t.Fatal("replaced root path accepted")
	}
}
