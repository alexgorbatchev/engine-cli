package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory"
	"github.com/alexgorbatchev/engine-cli/internal/playhistory/historytest"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// playedAPI serves a library whose only track was played twice in one session
// and once in a removed session, next to a play of a track the library lost.
func playedAPI(t *testing.T) (*Server, string) {
	t.Helper()
	s, root := testAPI(t)
	historytest.Write(t, root, historytest.Schema+`
		INSERT INTO Track(id,path,title,artist,length,originDatabaseUuid,originTrackId) VALUES
		(10,'song.mp3','Curated title','Artist',200,'fixture',1),(11,'gone.mp3','Gone','Other',100,'fixture',9);
		INSERT INTO Historylist(id,title,startTime,timezone,isDeleted) VALUES(1,'Friday',1700000000,'UTC',0),(2,'Removed',1700009000,'UTC',1);
		INSERT INTO HistorylistEntity(listId,trackId,startTime) VALUES(1,10,1700000100),(1,11,1700000200),(1,10,1700000300),(2,10,1700009100);`)
	return s, root
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return value
}

func errorCode(t *testing.T, body []byte) any {
	t.Helper()
	failure, _ := decodeBody(t, body)["error"].(map[string]any)
	return failure["code"]
}

func TestPlayedSessionRoutesReadHistoryWithCodedFailures(t *testing.T) {
	s, root := testAPI(t)
	w := requestAPI(t, s, http.MethodGet, "/v1/sessions", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"available":false,"sessions":[]}` {
		t.Fatalf("no history: %d %s", w.Code, w.Body)
	}
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		"/v1/sessions/1": {http.StatusNotFound, "play_history_unavailable"},
		"/v1/sessions/x": {http.StatusBadRequest, "invalid_session"},
		"/v1/sessions/0": {http.StatusBadRequest, "invalid_session"},
	} {
		w := requestAPI(t, s, http.MethodGet, path, "")
		if w.Code != want.status || errorCode(t, w.Body.Bytes()) != want.code {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	s, root = playedAPI(t)
	w = requestAPI(t, s, http.MethodGet, "/v1/sessions", "")
	list := decodeBody(t, w.Body.Bytes())
	sessions := list["sessions"].([]any)
	if w.Code != http.StatusOK || list["available"] != true || len(sessions) != 1 || sessions[0].(map[string]any)["track_count"] != float64(3) {
		t.Fatalf("sessions: %d %s", w.Code, w.Body)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/sessions/1", "")
	session := decodeBody(t, w.Body.Bytes())
	var resolved []any
	for _, entry := range session["entries"].([]any) {
		resolved = append(resolved, entry.(map[string]any)["track_id"])
	}
	if w.Code != http.StatusOK || session["title"] != "Friday" || !reflect.DeepEqual(resolved, []any{float64(1), nil, float64(1)}) {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"2", "404"} {
		w := requestAPI(t, s, http.MethodGet, "/v1/sessions/"+id, "")
		if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != "session_not_found" {
			t.Fatalf("session %s: %d %s", id, w.Code, w.Body)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Database2", playhistory.File), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sessions", "/v1/sessions/1"} {
		w := requestAPI(t, s, http.MethodGet, path, "")
		body := w.Body.String()
		if w.Code != http.StatusServiceUnavailable || errorCode(t, w.Body.Bytes()) != "play_history_unreadable" || strings.Contains(body, "damaged") {
			t.Fatalf("%s damaged: %d %s", path, w.Code, body)
		}
	}
	// Plays are optional on track records, so unreadable history leaves the collection readable.
	direct := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if track := decodeBody(t, direct.Body.Bytes()); direct.Code != http.StatusOK || track["playCount"] != nil {
		t.Fatalf("track with damaged history: %d %s", direct.Code, direct.Body)
	}
	if listed := runAPIJob(t, s, "track.list", "{}")["result"].([]any); len(listed) != 1 {
		t.Fatalf("track list with damaged history: %+v", listed)
	}
	if err := os.Remove(filepath.Join(root, "Database2", "m.db")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sessions", "/v1/sessions/1"} {
		w := requestAPI(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != "library_not_found" {
			t.Fatalf("%s without library: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestTrackRecordsReportPlaysOnlyWhenHistoryExists(t *testing.T) {
	s, _ := testAPI(t)
	plays := func(track map[string]any) [2]any { return [2]any{track["playCount"], track["lastPlayed"]} }
	listed := runAPIJob(t, s, "track.list", "{}")["result"].([]any)[0].(map[string]any)
	if got := plays(listed); got != [2]any{nil, nil} {
		t.Fatalf("without history: %v", got)
	}
	s, _ = playedAPI(t)
	want := [2]any{float64(2), float64(1700000300)}
	listed = runAPIJob(t, s, "track.list", "{}")["result"].([]any)[0].(map[string]any)
	inspected := runAPIJob(t, s, "track.inspect", `{"ids":[1]}`)["result"].(map[string]any)["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
	playlist := runAPIJob(t, s, "playlist.inspect", `{"ids":[1]}`)["result"].(map[string]any)
	direct := decodeBody(t, requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "").Body.Bytes())
	for name, track := range map[string]map[string]any{"list": listed, "inspect": inspected, "direct": direct} {
		if got := plays(track); got != want {
			t.Fatalf("%s plays = %v, want %v", name, got, want)
		}
	}
	if playlist["items"] == nil {
		t.Fatalf("playlist inspect: %+v", playlist)
	}
}

func TestSessionOperationsReadAndImportWithSnapshotAndUndo(t *testing.T) {
	s, root := playedAPI(t)
	s.config.NoBackup = false
	listed := runAPIJob(t, s, "session.list", "{}")["result"].(map[string]any)
	inspected := runAPIJob(t, s, "session.inspect", `{"session":1}`)["result"].(map[string]any)
	if listed["available"] != true || len(inspected["entries"].([]any)) != 3 {
		t.Fatalf("reads: %+v %+v", listed, inspected)
	}
	preview := runAPIJob(t, s, "session.import", `{"session":1,"skip_missing":true,"dry_run":true}`)
	if preview["history_id"] != nil {
		t.Fatal("preview created undo history")
	}
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote files: %v", err)
	}
	plan := preview["result"].(map[string]any)
	if plan["name"] != "Friday" || plan["duplicate_count"] != float64(1) || plan["missing_count"] != float64(1) || plan["can_apply"] != true {
		t.Fatalf("plan: %+v", plan)
	}
	job := runAPIJob(t, s, "session.import", fmt.Sprintf(`{"session":1,"skip_missing":true,"plan_token":%q}`, plan["plan_token"]))
	result := job["result"].(map[string]any)
	if result["imported_count"] != float64(1) || result["name"] != "Friday" || job["history_id"] == nil {
		t.Fatalf("import: %+v", job)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots: %+v %v", snapshots, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("undo entries: %+v %v", entries, err)
	}
	undo, _ := json.Marshal(map[string]any{"id": job["history_id"]})
	runAPIJob(t, s, "library.undo", string(undo))
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Playlist WHERE title='Friday'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("undo left the imported playlist: %d %v", count, err)
	}
}

func TestSessionImportProtectionAndRejectedReviewAreSideEffectFree(t *testing.T) {
	s, root := playedAPI(t)
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	result, id, err := s.RunOperation(context.Background(), "session.import", []byte(`{"session":1,"skip_missing":true,"dry_run":true}`))
	if err != nil || id != "" {
		t.Fatalf("protected read-only review: %v %s", err, id)
	}
	token := result.(*playhistory.ImportPlan).PlanToken
	for raw, want := range map[string]string{
		`{"session":1,"skip_missing":true,"plan_token":"unreviewed"}`:    "session_import_stale",
		`{"session":1,"plan_token":"` + token + `"}`:                     "session_import_stale",
		`{"session":1,"skip_missing":true,"plan_token":"` + token + `"}`: "engine_desktop_running",
	} {
		_, id, err := s.RunOperation(context.Background(), "session.import", []byte(raw))
		if progress.ErrorCode(err) != want || id != "" {
			t.Fatalf("%s: want %s got %v id %s", raw, want, err, id)
		}
		if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("rejection wrote snapshot or history: %v", err)
		}
	}
	if _, _, err := s.RunOperation(context.Background(), "session.inspect", []byte(`{"session":0}`)); err == nil {
		t.Fatal("accepted an invalid session")
	}
}
