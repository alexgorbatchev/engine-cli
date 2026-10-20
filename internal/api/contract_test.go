package api

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
)

func TestUndoPreviewDoesNotCreateBackup(t *testing.T) {
	s, _ := testAPI(t)
	created := runAPIJob(t, s, "playlist.create", `{"name":"Preview target"}`)
	id := created["history_id"].(string)
	s.config.NoBackup = false
	preview := runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"id":%q,"dry_run":true}`, id))
	if preview["history_id"] != nil {
		t.Fatalf("preview created a mutation history ID: %+v", preview)
	}
	snapshots, err := backup.ListBackups(s.config.LibraryDir, "")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("preview created a backup: %+v err=%v", snapshots, err)
	}
	runAPIJob(t, s, "playlist.inspect", `{"ids":[2]}`)
	runAPIJob(t, s, "library.undo", fmt.Sprintf(`{"id":%q}`, id))
	snapshots, err = backup.ListBackups(s.config.LibraryDir, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("real undo missing safety backup: %+v err=%v", snapshots, err)
	}
}

func TestConsumedTerminalCursorStopsReconnect(t *testing.T) {
	s, _ := testAPI(t)
	id := submitAPIJob(t, s, "playlist.list", `{}`)
	awaitAPIJob(t, s, id)
	s.mu.Lock()
	sequence := s.jobs[id].view.Progress.Sequence
	s.mu.Unlock()
	w := requestAPIWithCursor(t, s, id, strconv.FormatUint(sequence, 10))
	if w.Code != http.StatusNoContent {
		t.Fatalf("reconnect status=%d, want204", w.Code)
	}
}

func TestTrackResultsUseJSONNullsAndValues(t *testing.T) {
	s, _ := testAPI(t)
	runAPIJob(t, s, "playlist.track.add", `{"playlist":"1","tracks":["1"]}`)
	job := runAPIJob(t, s, "playlist.inspect", `{"ids":[1]}`)
	result := job["result"].(map[string]any)["items"].([]any)[0].(map[string]any)["data"].(map[string]any)
	tr := result["tracks"].([]any)[0].(map[string]any)
	if tr["title"] != "Curated title" || tr["album"] != nil || tr["id"] != float64(1) {
		t.Fatalf("non-native JSON track: %+v", tr)
	}
}
