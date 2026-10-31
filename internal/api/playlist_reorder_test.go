package api

import (
	"context"
	"database/sql"
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
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func reorderAPIFixture(t *testing.T) (*Server, *sql.DB, string, playlist.ReorderOptions) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	if _, err := db.CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, title := range []string{"Target", "Other"} {
		if _, err := db.CreatePlaylist(ctx, d, title, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	for id := int64(1); id <= 3; id++ {
		if _, err := d.ExecContext(ctx, "INSERT INTO Track(id,path,title) VALUES(?,?,?)", id, fmt.Sprintf("%d.wav", id), fmt.Sprintf("Track %d", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.AddTrackToPlaylist(ctx, d, 1, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AddTrackToPlaylist(ctx, d, 2, 2); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: root, CheckSystem: desktopClosed})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, d, root, playlist.ReorderOptions{Playlist: "1", Rows: []int{0}, Drop: 3, DryRun: true}
}

func reorderBody(t *testing.T, opts playlist.ReorderOptions) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertPlaylistOrder(t *testing.T, d *sql.DB, id int64, want []int64) {
	t.Helper()
	tracks, err := db.GetPlaylistTracks(context.Background(), d, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(tracks))
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("playlist %d: got %v want %v", id, ids, want)
	}
}

func TestPlaylistReorderAPIReviewApplyAndUndo(t *testing.T) {
	s, d, root, opts := reorderAPIFixture(t)
	ctx := context.Background()
	preview, id, err := s.RunOperation(ctx, "playlist.reorder", reorderBody(t, opts))
	if err != nil || id != "" {
		t.Fatalf("preview: %v %s %v", preview, id, err)
	}
	plan := preview.(*playlist.ReorderPlan)
	if !plan.CanApply || !reflect.DeepEqual(plan.TrackIDs, []int64{2, 3, 1}) {
		t.Fatalf("plan: %+v", plan)
	}
	assertPlaylistOrder(t, d, 1, []int64{1, 2, 3})
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview created safety artifacts: %v", err)
	}
	opts.DryRun, opts.PlanToken = false, plan.PlanToken
	job := runAPIJob(t, s, "playlist.reorder", string(reorderBody(t, opts)))
	result := job["result"].(map[string]any)
	if result["playlist_id"] != float64(1) || !reflect.DeepEqual(result["track_ids"], []any{float64(2), float64(3), float64(1)}) {
		t.Fatalf("result: %+v", result)
	}
	assertPlaylistOrder(t, d, 1, []int64{2, 3, 1})
	assertPlaylistOrder(t, d, 2, []int64{2})
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots: %+v %v", snapshots, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 || entries[0].ID != job["history_id"] {
		t.Fatalf("history: %+v %v", entries, err)
	}
	if _, err := d.Exec("UPDATE Track SET title='Later title' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	undo, err := json.Marshal(map[string]any{"id": job["history_id"]})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.undo", string(undo))
	assertPlaylistOrder(t, d, 1, []int64{1, 2, 3})
	assertPlaylistOrder(t, d, 2, []int64{2})
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Later title" {
		t.Fatalf("undo changed later metadata: %q %v", title, err)
	}
}

func TestPlaylistReorderAPIRefusesBeforeSafetyArtifacts(t *testing.T) {
	s, d, root, opts := reorderAPIFixture(t)
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	preview, _, err := s.RunOperation(context.Background(), "playlist.reorder", reorderBody(t, opts))
	if err != nil || checks != 0 {
		t.Fatalf("protected preview: %v checks=%d", err, checks)
	}
	plan := preview.(*playlist.ReorderPlan)
	opts.DryRun = false
	for _, test := range []struct{ token, code string }{{"", "playlist_reorder_stale"}, {"unreviewed", "playlist_reorder_stale"}, {plan.PlanToken, "engine_desktop_running"}} {
		opts.PlanToken = test.token
		result, id, err := s.RunOperation(context.Background(), "playlist.reorder", reorderBody(t, opts))
		if progress.ErrorCode(err) != test.code || result != nil || id != "" {
			t.Fatalf("want %s: result=%v id=%s error=%v", test.code, result, id, err)
		}
		if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("rejection created safety artifacts: %v", err)
		}
		assertPlaylistOrder(t, d, 1, []int64{1, 2, 3})
	}
	if checks != 1 {
		t.Fatalf("invalid review reached Desktop check: %d", checks)
	}
	opts.DryRun, opts.Drop = true, 1
	preview, _, err = s.RunOperation(context.Background(), "playlist.reorder", reorderBody(t, opts))
	if err != nil || preview.(*playlist.ReorderPlan).CanApply {
		t.Fatalf("unchanged preview: %v %v", preview, err)
	}
	opts.DryRun, opts.PlanToken = false, preview.(*playlist.ReorderPlan).PlanToken
	result, id, err := s.RunOperation(context.Background(), "playlist.reorder", reorderBody(t, opts))
	if progress.ErrorCode(err) != "playlist_reorder_unchanged" || result != nil || id != "" || checks != 1 {
		t.Fatalf("unchanged plan reached safety handling: %v %s %v checks=%d", result, id, err, checks)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, id, err = s.RunOperation(ctx, "playlist.reorder", reorderBody(t, opts))
	if err == nil || result != nil || id != "" || checks != 1 {
		t.Fatalf("cancelled request reached safety handling: %v %s %v checks=%d", result, id, err, checks)
	}
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("unchanged/cancelled plan created safety artifacts: %v", err)
	}
}

func TestPlaylistReorderAPIScopedLibraryIsolation(t *testing.T) {
	s, first, _, _ := reorderAPIFixture(t)
	other, second, root, opts := reorderAPIFixture(t)
	other.Close()
	opened := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, root))
	var libraries []openedLibrary
	if opened.Code != http.StatusOK || json.Unmarshal(opened.Body.Bytes(), &libraries) != nil || len(libraries) != 1 {
		t.Fatalf("open second library: %d %s", opened.Code, opened.Body)
	}
	prefix := libraries[0].URL
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = prefix + strings.TrimPrefix(r.URL.Path, "/v1")
		s.ServeHTTP(w, r)
	})
	for _, dryRun := range []bool{true, false} {
		opts.DryRun = dryRun
		response := requestAPI(t, s, http.MethodPost, prefix+"/operations/playlist.reorder", string(reorderBody(t, opts)))
		if response.Code != http.StatusAccepted || !strings.HasPrefix(response.Header().Get("Location"), prefix+"/") {
			t.Fatalf("scoped submit: %d %s", response.Code, response.Body)
		}
		var accepted Job
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		job := awaitAPIJob(t, scoped, accepted.ID)
		if job["state"] != "succeeded" {
			t.Fatalf("scoped job: %+v", job)
		}
		if dryRun {
			opts.PlanToken = job["result"].(map[string]any)["plan_token"].(string)
			assertPlaylistOrder(t, second, 1, []int64{1, 2, 3})
		}
	}
	assertPlaylistOrder(t, second, 1, []int64{2, 3, 1})
	assertPlaylistOrder(t, second, 2, []int64{2})
	assertPlaylistOrder(t, first, 1, []int64{1, 2, 3})
	assertPlaylistOrder(t, first, 2, []int64{2})
}

func TestPlaylistReorderAPIFailedPreviewHasNoResult(t *testing.T) {
	s, d, root, opts := reorderAPIFixture(t)
	opts.Rows = []int{8}
	result, id, err := s.RunOperation(context.Background(), "playlist.reorder", reorderBody(t, opts))
	if progress.ErrorCode(err) != "playlist_reorder_view" || result != nil || id != "" {
		t.Fatalf("failed preview leaked result: %v %s %v", result, id, err)
	}
	for _, body := range []string{`{"playlist":"1","rows":[0]}`, `{"playlist":"1","drop":0}`, `{"playlist":"1","rows":[0],"drop":0,"unexpected":true}`} {
		result, id, err := s.RunOperation(context.Background(), "playlist.reorder", json.RawMessage(body))
		if err == nil || result != nil || id != "" {
			t.Fatalf("invalid parameters accepted: %s: %v %s %v", body, result, id, err)
		}
	}
	assertPlaylistOrder(t, d, 1, []int64{1, 2, 3})
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("failed preview wrote safety artifacts: %v", err)
	}
}
