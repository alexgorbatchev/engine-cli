package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestRefreshCommandReportsPartialFailureAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		external bool
	}{
		{"human relative", "0", false},
		{"agent relative", "1", false},
		{"human external", "0", true},
		{"agent external", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT", tc.mode)
			lib := createTestLibrary(t)
			// Unreadable tags are expected per-file outcomes. Exclude host tools.
			t.Setenv("PATH", t.TempDir())
			if err := os.WriteFile(filepath.Join(lib, "second.mp3"), []byte("second"), 0600); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec("UPDATE Track SET fileBytes=0 WHERE id=1; INSERT INTO Track(id,path,fileBytes) VALUES(2,'second.mp3',0); CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END"); err != nil {
				t.Fatal(err)
			}
			if tc.external {
				path := filepath.Join(t.TempDir(), "external.mp3")
				if err := os.WriteFile(path, []byte("external audio"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := d.ExecContext(t.Context(), "UPDATE Track SET path=? WHERE id=1", path); err != nil {
					t.Fatal(err)
				}
			}
			out, err := executeCommand("-l", lib, "library", "refresh")
			if progress.ErrorCode(err) != "library_refresh_write_failed" {
				t.Fatalf("refresh error=%v output=%s", err, out)
			}
			want := "Updated Records:      1"
			if tc.mode == "1" {
				want = "updated_records: 1"
				if !strings.Contains(out, "status: incomplete") || !strings.Contains(out, "error_code: library_refresh_write_failed") {
					t.Fatalf("missing failed status: %s", out)
				}
			}
			if !strings.Contains(out, want) || !strings.Contains(out, "operation_id:") || strings.Contains(out, "Complete!") || strings.Contains(out, "status: complete") {
				t.Fatalf("dishonest or absent partial report: %s", out)
			}
			track, err := db.GetTrackByID(t.Context(), d, 1)
			if err != nil || track.FileBytes.Int64 == 0 {
				t.Fatalf("first update missing: track=%+v error=%v", track, err)
			}
			if _, err := executeCommand("-l", lib, "library", "undo"); err != nil {
				t.Fatal(err)
			}
			track, err = db.GetTrackByID(t.Context(), d, 1)
			if err != nil || track.FileBytes.Int64 != 0 {
				t.Fatalf("partial write not undone: track=%+v error=%v", track, err)
			}
		})
	}
}
