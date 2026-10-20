package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestAPIMergeAndUndoPreserveAudioAndMemberships(t *testing.T) {
	s, lib := testDuplicateAPI(t)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("original audio "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Source membership',0,0); INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,2,'api-fixture',0); UPDATE PerformanceData SET quickCues=x'0203',loops=x'04' WHERE trackId=2`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	merged := runAPIJob(t, s, "track.duplicate.merge", `{"keep":1,"remove":[2],"performance_from":2}`)
	historyID, ok := merged["history_id"].(string)
	if !ok || historyID == "" {
		t.Fatalf("merge omitted undo identity: %+v", merged)
	}
	assertMergeState(t, lib, true)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		assertMergeAudio(t, lib, name)
	}
	body, err := json.Marshal(map[string]string{"id": historyID})
	if err != nil {
		t.Fatal(err)
	}
	undone := runAPIJob(t, s, "library.undo", string(body))
	if undone["history_id"] == nil || undone["history_id"] == "" {
		t.Fatalf("undo omitted history identity: %+v", undone)
	}
	assertMergeState(t, lib, false)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		assertMergeAudio(t, lib, name)
	}
}

func assertMergeAudio(t *testing.T, lib, name string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(lib, name))
	if err != nil || string(got) != "original audio "+name {
		t.Fatalf("source audio changed %s: %q %v", name, got, err)
	}
}

func assertMergeState(t *testing.T, lib string, merged bool) {
	t.Helper()
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	removed, err := db.GetTrackByID(context.Background(), database, 2)
	if err != nil || (removed == nil) != merged {
		t.Fatalf("removed track after merged=%v: %+v %v", merged, removed, err)
	}
	var memberID int
	var uuid string
	if err := database.QueryRow("SELECT trackId,databaseUuid FROM PlaylistEntity WHERE listId=1").Scan(&memberID, &uuid); err != nil {
		t.Fatal(err)
	}
	wantMember := 2
	wantCues := string([]byte{1})
	if merged {
		wantMember = 1
		wantCues = string([]byte{2, 3})
	}
	if memberID != wantMember || uuid != "api-fixture" {
		t.Fatalf("playlist identity=%d/%s, want %d/api-fixture", memberID, uuid, wantMember)
	}
	var cues []byte
	if err := database.QueryRow("SELECT quickCues FROM PerformanceData WHERE trackId=1").Scan(&cues); err != nil || string(cues) != wantCues {
		t.Fatalf("keeper cues=%x want=%x error=%v", cues, []byte(wantCues), err)
	}
}

func TestAPIProgressFailureIsStringAndJobFailureIsStructured(t *testing.T) {
	s, _ := testAPI(t)
	id := submitAPIJob(t, s, "playlist.rm", `{"identifier":"missing"}`)
	job := awaitAPIJob(t, s, id)
	structured, ok := job["error"].(map[string]any)
	if !ok || structured["code"] != "operation_failed" || structured["message"] == "" {
		t.Fatalf("job error is not structured: %+v", job)
	}
	progress, ok := job["progress"].(map[string]any)
	if !ok {
		t.Fatalf("progress missing: %+v", job)
	}
	message, ok := progress["error"].(string)
	if !ok || message == "" || message != structured["message"] {
		t.Fatalf("progress error=%+v job error=%+v", progress, structured)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id+"/events", "")
	if w.Code != http.StatusOK {
		t.Fatalf("error replay status=%d body=%s", w.Code, w.Body)
	}
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == "error" {
			if event["error"] != message {
				t.Fatalf("SSE error is not the public string: %+v", event)
			}
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("terminal error event missing: %s", w.Body)
	}
}
