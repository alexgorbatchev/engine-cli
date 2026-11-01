package fixturecapture

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesAmbiguousComparison(t *testing.T) {
	for _, scenario := range []struct {
		name, before, after, message string
	}{
		{"missing identity", "CREATE TABLE Smartlist (rules TEXT)", "", "missing native row identity"},
		{"null identity", "CREATE TABLE Smartlist (listUuid TEXT); INSERT INTO Smartlist VALUES (NULL)", "", "invalid native row identity"},
		{"duplicate identity", "CREATE TABLE PlaylistEntity (id INTEGER); INSERT INTO PlaylistEntity VALUES (1),(1)", "", "duplicate native row identity"},
		{"changed columns", "CREATE TABLE Playlist (id INTEGER)", "CREATE TABLE Playlist (id INTEGER, title TEXT)", "matching columns"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := t.TempDir()
			for _, name := range []string{"before", "after"} {
				dir := filepath.Join(base, name)
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", sqliteURI(filepath.Join(dir, "m.db"), "rwc"))
				if err != nil {
					t.Fatal(err)
				}
				schema := scenario.before
				if name == "after" && scenario.after != "" {
					schema = scenario.after
				}
				_, execErr := db.Exec(schema)
				closeErr := db.Close()
				if execErr != nil || closeErr != nil {
					t.Fatalf("prepare: %v; close: %v", execErr, closeErr)
				}
			}
			out := filepath.Join(base, "capture")
			if _, err := Capture(t.Context(), filepath.Join(base, "before"), filepath.Join(base, "after"), out); err == nil || !strings.Contains(err.Error(), scenario.message) {
				t.Fatalf("ambiguous comparison accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(out, "complete.json")); !os.IsNotExist(err) {
				t.Fatalf("refusal published completion: %v", err)
			}
		})
	}
}

func TestCaptureRefusesUnsupportedFamilies(t *testing.T) {
	for _, files := range [][]string{{"notes.txt"}, {"m.db", "m.db-shm"}, {"m.db", "m.db-wal", "m.db-journal"}} {
		t.Run(strings.Join(files, "+"), func(t *testing.T) {
			base := t.TempDir()
			for _, name := range []string{"before", "after"} {
				dir := filepath.Join(base, name)
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					if err := os.WriteFile(filepath.Join(dir, file), []byte("unsupported family"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			out := filepath.Join(base, "capture")
			if _, err := Capture(t.Context(), filepath.Join(base, "before"), filepath.Join(base, "after"), out); err == nil {
				t.Fatal("unsupported family accepted")
			}
			if _, err := os.Stat(filepath.Join(out, "complete.json")); !os.IsNotExist(err) {
				t.Fatalf("refusal published completion: %v", err)
			}
		})
	}
}
