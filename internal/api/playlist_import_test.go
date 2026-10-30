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
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestPlaylistImportScopedEndpointIsolation(t *testing.T) {
	s, first, _ := apiImportFixture(t)
	other, second, opts := apiImportFixture(t)
	other.Close()
	opened := requestAPI(t, s, http.MethodPost, "/v1/libraries", fmt.Sprintf(`{"paths":[%q]}`, second))
	var libraries []openedLibrary
	if err := json.Unmarshal(opened.Body.Bytes(), &libraries); err != nil || len(libraries) != 1 {
		t.Fatalf("open second: %s %v", opened.Body, err)
	}
	prefix := libraries[0].URL
	for _, dryRun := range []bool{true, false} {
		opts.DryRun = dryRun
		raw, _ := json.Marshal(opts)
		response := requestAPI(t, s, http.MethodPost, prefix+"/operations/playlist.import", string(raw))
		if response.Code != http.StatusAccepted || !strings.HasPrefix(response.Header().Get("Location"), prefix+"/") {
			t.Fatalf("submit: %d %s", response.Code, response.Body)
		}
		var accepted Job
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Path = prefix + strings.TrimPrefix(r.URL.Path, "/v1")
			s.ServeHTTP(w, r)
		})
		job := awaitAPIJob(t, scoped, accepted.ID)
		if job["state"] != "succeeded" {
			t.Fatalf("job: %+v", job)
		}
		if dryRun {
			opts.PlanToken = job["result"].(map[string]any)["plan_token"].(string)
		}
	}
	for _, fixture := range []struct {
		root  string
		count int
	}{{first, 1}, {second, 2}} {
		d, err := db.Open(filepath.Join(fixture.root, "Database2", "m.db"))
		if err != nil {
			t.Fatal(err)
		}
		var count int
		err = d.QueryRow("SELECT COUNT(*) FROM Playlist").Scan(&count)
		d.Close()
		if err != nil || count != fixture.count {
			t.Fatalf("library isolation: got %d want %d: %v", count, fixture.count, err)
		}
	}
}

func apiImportFixture(t *testing.T) (*Server, string, playlist.ImportOptions) {
	t.Helper()
	s, root := testAPI(t)
	if err := os.WriteFile(filepath.Join(root, "song.mp3"), []byte("untouched audio"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "set.m3u8")
	if err := os.WriteFile(source, []byte("#EXTM3U\nsong.mp3\nsong.mp3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return s, root, playlist.ImportOptions{Path: source, Name: "Imported", DryRun: true}
}

func TestPlaylistImportSnapshotAndUndo(t *testing.T) {
	s, root, opts := apiImportFixture(t)
	s.config.NoBackup = false
	raw, _ := json.Marshal(opts)
	preview := runAPIJob(t, s, "playlist.import", string(raw))
	if preview["history_id"] != nil {
		t.Fatal("preview created history")
	}
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote files: %v", err)
	}
	plan := preview["result"].(map[string]any)
	opts.DryRun = false
	opts.PlanToken = plan["plan_token"].(string)
	raw, _ = json.Marshal(opts)
	job := runAPIJob(t, s, "playlist.import", string(raw))
	result := job["result"].(map[string]any)
	if result["imported_count"] != float64(1) || result["duplicate_count"] != float64(1) || !reflect.DeepEqual(result["track_ids"], []any{float64(1)}) {
		t.Fatal(job)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots: %+v %v", snapshots, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("undo entries: %+v %v", entries, err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title='Later curated title' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	undo, _ := json.Marshal(map[string]any{"id": job["history_id"]})
	runAPIJob(t, s, "library.undo", string(undo))
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Playlist").Scan(&count); err != nil || count != 1 {
		t.Fatalf("undo playlists: %d %v", count, err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&title); err != nil || title != "Later curated title" {
		t.Fatalf("undo damaged unrelated edit: %s %v", title, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "song.mp3")); err != nil || string(data) != "untouched audio" {
		t.Fatalf("audio changed: %q %v", data, err)
	}
}

func TestPlaylistImportProtectionAndRejectedReviewAreSideEffectFree(t *testing.T) {
	s, root, opts := apiImportFixture(t)
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	raw, _ := json.Marshal(opts)
	result, id, err := s.RunOperation(context.Background(), "playlist.import", raw)
	if err != nil || id != "" {
		t.Fatalf("protected read-only review: %v %s", err, id)
	}
	opts.DryRun = false
	opts.PlanToken = result.(*playlist.ImportPlan).PlanToken
	for _, token := range []string{"unreviewed", opts.PlanToken} {
		opts.PlanToken = token
		raw, _ = json.Marshal(opts)
		_, id, err = s.RunOperation(context.Background(), "playlist.import", raw)
		want := "playlist_import_stale"
		if token != "unreviewed" {
			want = "engine_desktop_running"
		}
		if progress.ErrorCode(err) != want || id != "" {
			t.Fatalf("want %s got %v id%s", want, err, id)
		}
		if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("rejection wrote snapshot/history: %v", err)
		}
	}
}
