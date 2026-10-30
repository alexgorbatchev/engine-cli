package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestCapitalizationCommandReviewApplyAndOutput(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title='istanbul' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	args := []string{"track", "batch", "capitalize", "1", "--fields", "title", "--recipe", "upper", "--language", "tr"}
	preview := runJSON(t, lib, append(append([]string{}, args...), "--dry-run", "--json")...)
	token, ok := preview["plan_token"].(string)
	if !ok || token == "" || preview["changed_fields"] != float64(1) {
		t.Fatal(preview)
	}
	text, err := executeCommand(append(append([]string{"-l", lib, "--no-backup"}, args...), "--dry-run")...)
	if err != nil || !strings.Contains(text, `"istanbul" -> "İSTANBUL"`) {
		t.Fatalf("review=%s %v", text, err)
	}
	command := newRootCommand()
	var out, diagnostics bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&diagnostics)
	command.SetArgs(append(append([]string{"-l", lib, "--no-backup"}, args...), "--plan-token", token, "--json"))
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["changed_fields"] != float64(1) || !strings.Contains(diagnostics.String(), "operation_id:") {
		t.Fatalf("stdout=%s stderr=%s %v", &out, &diagnostics, err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "İSTANBUL" {
		t.Fatalf("title=%s %v", title, err)
	}
	for _, invalid := range [][]string{
		{"track", "batch", "capitalize", "1", "--fields", "title", "--dry-run"},
		{"track", "batch", "capitalize", "1", "--fields", "path", "--recipe", "upper", "--dry-run"},
		{"track", "batch", "capitalize", "1", "--fields", "title", "--recipe", "lower"},
	} {
		if _, err := executeCommand(append([]string{"-l", lib}, invalid...)...); err == nil {
			t.Fatalf("invalid command accepted: %v", invalid)
		}
	}
}
