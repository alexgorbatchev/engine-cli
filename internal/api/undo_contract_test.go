package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func undoRequestBody(t *testing.T, id string, force, preview bool) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": id, "force": force, "dry_run": preview})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func historyIdentity(t *testing.T, job map[string]any) string {
	t.Helper()
	id, ok := job["history_id"].(string)
	if !ok || id == "" {
		t.Fatalf("job has no history identity: %+v", job)
	}
	return id
}

func expectUndoFailure(t *testing.T, s *Server, body, contains string) map[string]any {
	t.Helper()
	id := submitAPIJob(t, s, "library.undo", body)
	job := awaitAPIJob(t, s, id)
	failure, ok := job["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if job["state"] != "failed" || !ok || !strings.Contains(message, contains) {
		t.Fatalf("expected undo failure containing %q: %+v", contains, job)
	}
	return job
}

func TestAPIUndoEmptyAndUnknownHistory(t *testing.T) {
	s, _ := testAPI(t)
	job := runAPIJob(t, s, "library.history", `{}`)
	entries, ok := job["result"].([]any)
	if !ok || len(entries) != 0 {
		t.Fatalf("empty history=%+v", job)
	}
	expectUndoFailure(t, s, `{}`, "not found")
	expectUndoFailure(t, s, undoRequestBody(t, "unknown", false, false), "not found")
	job = runAPIJob(t, s, "library.history", `{}`)
	entries, ok = job["result"].([]any)
	if !ok || len(entries) != 0 {
		t.Fatalf("failed lookup altered history=%+v", job)
	}
}

func TestAPIUndoDatabaseConflictPreviewForceAndRedo(t *testing.T) {
	s, lib := testAPI(t)
	created := runAPIJob(t, s, "playlist.create", `{"name":"Original insertion"}`)
	id := historyIdentity(t, created)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Playlist SET title='Later conflicting edit' WHERE title='Original insertion'; UPDATE Playlist SET title='Unrelated later edit' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{false, true} {
		expectUndoFailure(t, s, undoRequestBody(t, id, false, preview), "conflict")
		assertUndoPlaylist(t, lib, "Later conflicting edit", true)
		assertUndoPlaylist(t, lib, "Unrelated later edit", true)
	}
	runAPIJob(t, s, "library.undo", undoRequestBody(t, id, true, true))
	assertUndoPlaylist(t, lib, "Later conflicting edit", true)
	undone := runAPIJob(t, s, "library.undo", undoRequestBody(t, id, true, false))
	assertUndoPlaylist(t, lib, "Later conflicting edit", false)
	assertUndoPlaylist(t, lib, "Unrelated later edit", true)
	expectUndoFailure(t, s, undoRequestBody(t, id, false, false), "already been undone")
	runAPIJob(t, s, "library.undo", undoRequestBody(t, historyIdentity(t, undone), false, false))
	assertUndoPlaylist(t, lib, "Later conflicting edit", true)
	assertUndoPlaylist(t, lib, "Unrelated later edit", true)
}

func assertUndoPlaylist(t *testing.T, lib, title string, present bool) {
	t.Helper()
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pl, err := db.FindPlaylist(context.Background(), database, title)
	if err != nil || (pl != nil) != present {
		t.Fatalf("playlist %q present=%v got=%+v error=%v", title, present, pl, err)
	}
}

func TestAPIUndoMovedAudioConflict(t *testing.T) {
	s, lib := testAPI(t)
	original := filepath.Join(lib, "song.mp3")
	moved := filepath.Join(lib, "moved.mp3")
	if err := os.WriteFile(original, []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"identifier": "1", "destination": moved})
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.move", string(body))
	id := historyIdentity(t, job)
	if err := os.WriteFile(moved, []byte("later audio edit"), 0600); err != nil {
		t.Fatal(err)
	}
	expectUndoFailure(t, s, undoRequestBody(t, id, false, true), "conflict")
	assertUndoFile(t, moved, "later audio edit")
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("preview restored original: %v", err)
	}
	runAPIJob(t, s, "library.undo", undoRequestBody(t, id, true, true))
	assertUndoFile(t, moved, "later audio edit")
	undone := runAPIJob(t, s, "library.undo", undoRequestBody(t, id, true, false))
	assertUndoFile(t, original, "original audio")
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatalf("undo left moved audio: %v", err)
	}
	runAPIJob(t, s, "library.undo", undoRequestBody(t, historyIdentity(t, undone), false, false))
	assertUndoFile(t, moved, "later audio edit")
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("redo left original audio: %v", err)
	}
}

func assertUndoFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s=%q want=%q error=%v", path, got, want, err)
	}
}

func TestAPICancelledMutationCanBeUndone(t *testing.T) {
	s, lib := testAPI(t)
	written := make(chan struct{})
	s.operations["test.partial"] = define("test.partial", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
		if err != nil {
			return nil, err
		}
		if _, err := database.ExecContext(ctx, "UPDATE Track SET comment='partial change' WHERE id=1"); err != nil {
			database.Close()
			return nil, err
		}
		if err := database.Close(); err != nil {
			return nil, err
		}
		close(written)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	id := submitAPIJob(t, s, "test.partial", `{}`)
	waitSignal(t, written)
	w := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("cancel status=%d body=%s", w.Code, w.Body)
	}
	job := awaitAPIJob(t, s, id)
	if job["state"] != "cancelled" {
		t.Fatalf("mutation state=%+v", job)
	}
	runAPIJob(t, s, "library.undo", undoRequestBody(t, historyIdentity(t, job), false, false))
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	track, err := db.GetTrackByID(context.Background(), database, 1)
	if err != nil || track.Comment.Valid {
		t.Fatalf("partial mutation not undone: %+v error=%v", track, err)
	}
}
