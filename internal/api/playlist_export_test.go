package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestPlaylistExportReviewAndExclusiveApply(t *testing.T) {
	s, root, _ := apiImportFixture(t)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTrackToPlaylist(context.Background(), d, 1, 1); err != nil {
		t.Fatal(err)
	}
	d.Close()
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		t.Fatal("read-only collection export checked Desktop")
		return nil, nil
	}
	path := filepath.Join(root, "export.m3u8")
	params := map[string]any{"identifier": "1", "path": path, "dry_run": true}
	raw, _ := json.Marshal(params)
	result, historyID, err := s.RunOperation(context.Background(), "playlist.export", raw)
	if err != nil || historyID != "" {
		t.Fatalf("review failed: %v history %s", err, historyID)
	}
	encoded, _ := json.Marshal(result)
	var plan map[string]any
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview created output: %v", err)
	}
	params["dry_run"] = false
	params["plan_token"] = plan["plan_token"]
	raw, _ = json.Marshal(params)
	_, historyID, err = s.RunOperation(context.Background(), "playlist.export", raw)
	if err != nil || historyID != "" {
		t.Fatalf("apply failed: %v history %s", err, historyID)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(data), "#EXTM3U\n") || !strings.Contains(string(data), "song.mp3") {
		t.Fatalf("serialized playlist: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("export created source history or backups: %v", err)
	}
	if _, _, err := s.RunOperation(context.Background(), "playlist.export", raw); err == nil {
		t.Fatal("existing export was replaced")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(data) {
		t.Fatalf("existing destination changed: %q %v", after, err)
	}
}

func TestPlaylistExportFailureHasNoResult(t *testing.T) {
	s, root, _ := apiImportFixture(t)
	path := filepath.Join(root, "occupied.m3u8")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"identifier": "1", "path": path, "dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	result, id, err := s.RunOperation(context.Background(), "playlist.export", raw)
	if progress.ErrorCode(err) != "playlist_export_exists" || result != nil || id != "" {
		t.Fatalf("failed review returned a result: %#v %q %v", result, id, err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "playlist.export", string(raw)))
	if job["state"] != "failed" || job["result"] != nil || job["history_id"] != nil {
		t.Fatalf("failed export returned changes: %v", job)
	}
	failure := job["error"].(map[string]any)
	if failure["code"] != "playlist_export_exists" {
		t.Fatalf("lost safe export code: %v", job)
	}
}
