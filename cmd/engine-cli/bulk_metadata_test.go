package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestBulkMetadataCommand(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO Track(id,title,path) VALUES(2,'Second','second.mp3')"); err != nil {
		t.Fatal(err)
	}
	result := runJSON(t, lib, "track", "batch", "update", "1", "2", "--artist", "Together", "--key", "8A", "--json")
	if !reflect.DeepEqual(result["track_ids"], []any{float64(1), float64(2)}) {
		t.Fatal(result)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track WHERE artist='Together' AND key=1").Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		if _, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "2", "--genre", "House"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "999", "--title", "Missing"); err == nil {
		t.Fatal("accepted missing track")
	}
}

func TestBulkMetadataCommandReviewsBeforeApplying(t *testing.T) {
	t.Setenv("AGENT", "0")
	lib := createTestLibrary(t)
	plan := runJSON(t, lib, "track", "batch", "update", "1", "--genre", "House", "--dry-run", "--json")
	token, _ := plan["plan_token"].(string)
	if token == "" || plan["can_apply"] != true || plan["changed_fields"] != float64(1) {
		t.Fatal(plan)
	}
	review, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "999", "--genre", "House", "--dry-run")
	if err != nil || !strings.Contains(review, `Track 1, genre: (empty) -> "House"`) || !strings.Contains(review, "Not in this library: 999") || !strings.Contains(review, "Cannot apply") {
		t.Fatalf("review=%s err=%v", review, err)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 0 {
		t.Fatalf("review recorded history: %v %v", entries, err)
	}
	applied, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "--genre", "House", "--plan-token", token)
	if err != nil || !strings.Contains(applied, "Updated 1 fields across 1 tracks") {
		t.Fatalf("apply=%s err=%v", applied, err)
	}
	if _, err := executeCommand("-l", lib, "--no-backup", "track", "batch", "update", "1", "--genre", "Techno", "--plan-token", token); err == nil {
		t.Fatal("applied a stale review")
	}
}
