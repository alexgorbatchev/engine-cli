package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func historyEntries(t *testing.T, s *Server) []map[string]any {
	t.Helper()
	job := runAPIJob(t, s, "library.history", `{}`)
	raw, err := json.Marshal(job["result"])
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func historyEntry(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	for _, entry := range historyEntries(t, s) {
		if entry["id"] == id {
			return entry
		}
	}
	t.Fatalf("history has no entry %s", id)
	return nil
}

func addSummaryTrack(t *testing.T, lib string) {
	t.Helper()
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO Track(id,title,artist,path,genre) VALUES(2,'Ritual Shadows','Artist','two.mp3','Techno')"); err != nil {
		t.Fatal(err)
	}
}

func TestAPIHistorySummaryNamesTracksFieldsAndUniformValues(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	job := runAPIJob(t, s, "track.batch.update", `{"identifiers":["1","2"],"genre":"House","year":2020,"history_source":"Warmup","group_id":"run-1"}`)
	entry := historyEntry(t, s, historyIdentity(t, job))
	if entry["source"] != "Warmup" || entry["group_id"] != "run-1" {
		t.Fatalf("entry lost its caller description: %+v", entry)
	}
	summary, ok := entry["summary"].(map[string]any)
	if !ok {
		t.Fatalf("entry has no summary: %+v", entry)
	}
	if summary["tracks"] != float64(2) || summary["playlists"] != float64(0) {
		t.Fatalf("summary counts=%+v", summary)
	}
	names, _ := summary["track_names"].([]any)
	if len(names) != 2 || names[0] != "Curated title" || names[1] != "Ritual Shadows" {
		t.Fatalf("summary names=%+v", summary["track_names"])
	}
	fields, _ := summary["fields"].([]any)
	byField := map[string]map[string]any{}
	for _, item := range fields {
		field := item.(map[string]any)
		byField[field["field"].(string)] = field
	}
	if genre := byField["genre"]; genre == nil || genre["uniform"] != true || genre["value"] != "House" {
		t.Fatalf("genre field=%+v", byField)
	}
	if year := byField["year"]; year == nil || year["uniform"] != true || year["value"] != float64(2020) {
		t.Fatalf("year field=%+v", byField)
	}
	if _, ok := byField["lastEditTime"]; ok {
		t.Fatalf("bookkeeping column reported: %+v", byField)
	}
}

func TestAPIHistorySummaryReportsMixedValuesAndAddedPlaylists(t *testing.T) {
	s, lib := testAPI(t)
	addSummaryTrack(t, lib)
	runAPIJob(t, s, "track.update", `{"identifier":"2","title":"Renamed"}`)
	job := runAPIJob(t, s, "playlist.create", `{"name":"Warmup"}`)
	summary := historyEntry(t, s, historyIdentity(t, job))["summary"].(map[string]any)
	if summary["added_playlists"] != float64(1) || summary["playlists"] != float64(1) {
		t.Fatalf("playlist summary=%+v", summary)
	}
	if names, _ := summary["playlist_names"].([]any); len(names) != 1 || names[0] != "Warmup" {
		t.Fatalf("playlist names=%+v", summary)
	}
}

func TestAPIHistoryLinksTheUndoThatUndidAnEntry(t *testing.T) {
	s, _ := testAPI(t)
	job := runAPIJob(t, s, "track.update", `{"identifier":"1","genre":"House"}`)
	id := historyIdentity(t, job)
	if entry := historyEntry(t, s, id); entry["undone_by"] != nil {
		t.Fatalf("live entry reported as undone: %+v", entry)
	}
	undone := runAPIJob(t, s, "library.undo", undoRequestBody(t, id, false, false))
	undoID := historyIdentity(t, undone)
	if entry := historyEntry(t, s, id); entry["undone_by"] != undoID {
		t.Fatalf("undone entry=%+v want undone_by %s", entry, undoID)
	}
	redone := runAPIJob(t, s, "library.undo", undoRequestBody(t, undoID, false, false))
	if entry := historyEntry(t, s, undoID); entry["undone_by"] != historyIdentity(t, redone) {
		t.Fatalf("redone undo entry=%+v", entry)
	}
}

func TestAPIHistoryEnvelopeIsValidatedAndListedOnlyForWrites(t *testing.T) {
	s, _ := testAPI(t)
	for _, body := range []string{`{"identifier":"1","genre":"House","group_id":7}`, `{"identifier":"1","history_source":"` + strings.Repeat("x", 201) + `"}`} {
		if w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.update", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid envelope accepted: %d %s", w.Code, w.Body)
		}
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.history", `{"group_id":"run"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("read accepted the history envelope: %d %s", w.Code, w.Body)
	}
	w := requestAPI(t, s, "GET", "/v1/operations", "")
	var catalog []operation
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("catalog=%s: %v", w.Body.String(), err)
	}
	seen := 0
	for _, op := range catalog {
		switch op.Name {
		case "track.update":
			seen++
			if len(op.HistoryParameters) != 2 {
				t.Fatalf("write lacks the history envelope: %+v", op)
			}
		case "library.history", "intake.pause":
			seen++
			if len(op.HistoryParameters) != 0 {
				t.Fatalf("read offers the history envelope: %+v", op)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("catalog=%s", w.Body.String())
	}
}
