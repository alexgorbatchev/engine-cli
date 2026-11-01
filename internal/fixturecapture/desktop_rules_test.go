package fixturecapture

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureDesktopWrittenRulesRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/desktop-5.1.0/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Cases []struct{ Name, Rules string }
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Cases) != 38 {
		t.Fatalf("incomplete handover: %d rules", len(golden.Cases))
	}
	base := t.TempDir()
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		database, err := sql.Open("sqlite", sqliteURI(filepath.Join(dir, "m.db"), "rwc"))
		if err != nil {
			t.Fatal(err)
		}
		_, schemaErr := database.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, Rules TEXT NOT NULL, nextListUuid TEXT)")
		if schemaErr != nil {
			t.Fatal(errors.Join(schemaErr, database.Close()))
		}
		for _, fixture := range golden.Cases {
			if _, err := database.Exec("INSERT INTO Smartlist VALUES (?, ?, NULL)", fixture.Name, fixture.Rules); err != nil {
				t.Fatal(errors.Join(err, database.Close()))
			}
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(base, "capture")
	if _, err := Capture(t.Context(), filepath.Join(base, "before"), filepath.Join(base, "after"), out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"before", "after"} {
		tables, err := readTables(filepath.Join(out, "snapshots", name, "m.db.json"))
		if err != nil {
			t.Fatal(err)
		}
		table := findTable(tables, "Smartlist")
		if table == nil || len(table.Rows) != len(golden.Cases) {
			t.Fatalf("%s lost Desktop rows", name)
		}
		rows, err := keyRows(table, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, fixture := range golden.Cases {
			key, err := json.Marshal(Value{Type: "text", Data: base64.StdEncoding.EncodeToString([]byte(fixture.Name))})
			if err != nil {
				t.Fatal(err)
			}
			row := rows[string(key)]
			if len(row) != 3 || row[1].Type != "text" || row[1].Data != base64.StdEncoding.EncodeToString([]byte(fixture.Rules)) || row[2].Type != "null" {
				t.Fatalf("%s/%s lost exact Rules TEXT or native NULL: %#v", name, fixture.Name, row)
			}
		}
	}
	changes, err := os.ReadFile(filepath.Join(out, "row-changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var comparison []Change
	if err := json.Unmarshal(changes, &comparison); err != nil {
		t.Fatal(err)
	}
	if len(comparison) != 0 {
		t.Fatalf("unchanged Desktop rules reported as changes: %d", len(comparison))
	}
}
