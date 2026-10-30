package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestFindReplaceCommandReviewAndApply(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title='Mix One' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	args := []string{"track", "batch", "find-replace", "1", "--fields", "title", "--find", "Mix", "--replacement", "Set"}
	plan := runJSON(t, lib, append(append([]string{}, args...), "--dry-run", "--json")...)
	token, ok := plan["plan_token"].(string)
	if !ok || token == "" || plan["changed_fields"] != float64(1) {
		t.Fatal(plan)
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
		t.Fatalf("stdout=%s stderr=%s err=%v", &out, &diagnostics, err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Set One" {
		t.Fatalf("title=%s err=%v", title, err)
	}
	args = []string{"-l", lib, "--no-backup", "track", "batch", "find-replace", "1", "--fields", "title", "--find", "Set", "--replacement", "Mix", "--dry-run"}
	text, err := executeCommand(args...)
	if err != nil || !strings.Contains(text, `"Set One" -> "Mix One"`) || !strings.Contains(text, "Plan token:") {
		t.Fatalf("output=%s err=%v", text, err)
	}
	if _, err := executeCommand("-l", lib, "track", "batch", "find-replace", "1", "--fields", "artist", "--find", "DJ", "--dry-run"); err == nil {
		t.Fatal("accepted omitted replacement")
	}
	if _, err := executeCommand("-l", lib, "track", "batch", "find-replace", "1", "--fields", "artist", "--find", "DJ", "--replacement", "", "--dry-run"); err != nil {
		t.Fatal(err)
	}
}
