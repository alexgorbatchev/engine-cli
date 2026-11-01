package fixturecapture

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureNestedFamiliesAndRemovedDatabase(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(base, name, "Database2")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		paths := []string{"m.db"}
		if name == "before" {
			paths = append(paths, "removed.db")
		}
		for _, path := range paths {
			db, err := sql.Open("sqlite", sqliteURI(filepath.Join(dir, path), "rwc"))
			if err != nil {
				t.Fatal(err)
			}
			_, execErr := db.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, rules TEXT); INSERT INTO Smartlist VALUES ('synthetic', '{}')")
			closeErr := db.Close()
			if execErr != nil || closeErr != nil {
				t.Fatalf("prepare: %v; close: %v", execErr, closeErr)
			}
		}
	}
	out := filepath.Join(base, "capture")
	report, err := Capture(t.Context(), filepath.Join(base, "before"), filepath.Join(base, "after"), out)
	if err != nil {
		t.Fatal(err)
	}
	if report.Before.Directories["Database2"] == "" || len(report.Before.Files) != 2 || len(report.After.Files) != 1 {
		t.Fatalf("nested inventory lost: %#v", report)
	}
	data, err := os.ReadFile(filepath.Join(out, "row-changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var changes []Change
	if err := json.Unmarshal(data, &changes); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Database != filepath.Join("Database2", "removed.db") || changes[0].After != nil || len(changes[0].Before) != 2 {
		t.Fatalf("removed database rows lost: %#v", changes)
	}
	if err := os.Remove(filepath.Join(out, "snapshots", "before", "Database2", "m.db.json")); err != nil {
		t.Fatal(err)
	}
	if err := compareImages(out, report.Before, report.After); err == nil {
		t.Fatal("missing captured export accepted")
	}
}
