package main

import (
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory/historytest"
)

func playedLibrary(t *testing.T) string {
	t.Helper()
	root := createTestLibrary(t)
	historytest.Write(t, root, historytest.Schema+`
		INSERT INTO Track(id,path,title,artist,originDatabaseUuid,originTrackId) VALUES
		(10,'Music/track1.m4a','Sample Title','Sample Artist','fixture',1),(11,'Music/gone.mp3','Gone','Other','fixture',9);
		INSERT INTO Historylist(id,title,startTime,timezone,isDeleted) VALUES(1,NULL,1700000000,'UTC',0);
		INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES(1,10,1700000100),(1,11,1700000200),(1,10,1700000300);`)
	return root
}

func TestSessionCommandsWithoutHistory(t *testing.T) {
	root := createTestLibrary(t)
	out, err := executeCommand("-l", root, "session", "list")
	if err != nil || !strings.Contains(out, "This library has no play history yet.") {
		t.Fatalf("list: %s %v", out, err)
	}
	if out, err := executeCommand("-l", root, "session", "inspect", "1"); err == nil {
		t.Fatalf("inspect without history succeeded: %s", out)
	}
	for _, id := range []string{"0", "set"} {
		if _, err := executeCommand("-l", root, "session", "inspect", id); err == nil || !strings.Contains(err.Error(), "positive history session ID") {
			t.Fatalf("inspect %s: %v", id, err)
		}
	}
}

func TestSessionCommandsListInspectAndImport(t *testing.T) {
	root := playedLibrary(t)
	out, err := executeCommand("-l", root, "session", "list")
	if err != nil || !strings.Contains(out, "14/11/2023 22:13 (UTC)") || !strings.Contains(out, "2023-11-14 22:13") || strings.Contains(out, "{") {
		t.Fatalf("list: %s %v", out, err)
	}
	out, err = executeCommand("-l", root, "session", "inspect", "1")
	for _, text := range []string{"(3 tracks)", "Sample Title", "Gone", "missing"} {
		if err != nil || !strings.Contains(out, text) {
			t.Fatalf("inspect missing %q: %s %v", text, out, err)
		}
	}
	listed := runJSON(t, root, "session", "list", "--json")
	if listed["available"] != true {
		t.Fatalf("json list: %+v", listed)
	}
	args := []string{"-l", root, "--no-backup", "session", "import", "1", "--name", "Friday"}
	preview, err := executeCommand(append(append([]string{}, args...), "--dry-run")...)
	if err != nil || !strings.Contains(preview, "Not applicable as reviewed") || !strings.Contains(preview, "duplicate") {
		t.Fatalf("blocked review: %s %v", preview, err)
	}
	args = append(args, "--skip-missing")
	preview, err = executeCommand(append(append([]string{}, args...), "--dry-run")...)
	if err != nil || !strings.Contains(preview, `New playlist "Friday"`) || !strings.Contains(preview, "plan_token:") {
		t.Fatalf("review: %s %v", preview, err)
	}
	plan := runJSON(t, root, "session", "import", "1", "--name", "Friday", "--skip-missing", "--dry-run", "--json")
	if _, err := executeCommand(args...); err == nil {
		t.Fatal("import without a reviewed plan succeeded")
	}
	out, err = executeCommand(append(args, "--plan-token", plan["plan_token"].(string))...)
	if err != nil || !strings.Contains(out, `"Friday": 1 tracks, 1 repeated plays omitted, 1 missing tracks skipped`) {
		t.Fatalf("apply: %s %v", out, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("undo history: %+v %v", entries, err)
	}
	inspected := runJSON(t, root, "track", "inspect", "1", "--json")
	data := inspected["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
	if data["playCount"] != float64(2) || data["lastPlayed"] != float64(1700000300) {
		t.Fatalf("track plays: %+v", data)
	}
}

func TestSessionOutputForEmptyHistoryAndUnresolvedPlans(t *testing.T) {
	var out strings.Builder
	if handled, err := printSession(&out, &playhistory.Sessions{Available: true, Sessions: []playhistory.Session{}}, false); !handled || err != nil {
		t.Fatalf("sessions: %v %v", handled, err)
	}
	if handled, _ := printSession(&out, &playhistory.Sessions{Available: true}, true); handled {
		t.Fatal("JSON output was printed as a table")
	}
	plan := &playhistory.ImportPlan{Name: "Set", SessionTitle: "Set", Entries: []playhistory.Entry{{Position: 1, Title: "Gone", Status: "missing"}}}
	if _, err := printSession(&out, plan, false); err != nil {
		t.Fatal(err)
	}
	played := int64(1700000000)
	if got := sessionTime(&played, "Not/AZone"); got != time.Unix(played, 0).Local().Format("2006-01-02 15:04") {
		t.Fatalf("unknown zone time = %q", got)
	}
	for _, text := range []string{"No played sessions.", "Not applicable: none of this session's tracks are in the library."} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %q in %s", text, out.String())
		}
	}
	if help, err := executeCommand("session"); err != nil || !strings.Contains(help, "session import <id>") {
		t.Fatalf("help: %s %v", help, err)
	}
}
