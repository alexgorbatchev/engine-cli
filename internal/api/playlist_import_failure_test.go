package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestPlaylistImportFailuresHaveNoResult(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		name := "apply"
		if dryRun {
			name = "preview"
		}
		t.Run(name, func(t *testing.T) {
			s, root, opts := apiImportFixture(t)
			opts.Path = filepath.Join(t.TempDir(), "external.m3u8")
			if err := os.WriteFile(opts.Path, []byte(filepath.Join(root, "song.mp3")+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if !dryRun {
				raw, err := json.Marshal(opts)
				if err != nil {
					t.Fatal(err)
				}
				result, _, err := s.RunOperation(context.Background(), "playlist.import", raw)
				if err != nil {
					t.Fatal(err)
				}
				opts.PlanToken = result.(*playlist.ImportPlan).PlanToken
				// The source can disappear after review validation and before apply.
				s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
					if err := os.Remove(opts.Path); err != nil {
						return nil, err
					}
					return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
				}
			} else if err := os.Remove(opts.Path); err != nil {
				t.Fatal(err)
			}
			opts.DryRun = dryRun
			raw, err := json.Marshal(opts)
			if err != nil {
				t.Fatal(err)
			}
			result, id, err := s.RunOperation(context.Background(), "playlist.import", raw)
			if progress.ErrorCode(err) != "playlist_import_unavailable" || result != nil {
				t.Fatalf("failed import returned a result: result=%#v id=%q err=%v", result, id, err)
			}
			entries, err := history.List(root)
			if err != nil {
				t.Fatal(err)
			}
			if dryRun {
				if id != "" || len(entries) != 0 {
					t.Fatalf("failed preview created undo history: %v %q", entries, id)
				}
			} else if len(entries) != 1 || entries[0].ID != id || entries[0].State != "failed" {
				t.Fatalf("failed apply lost its recovery history: %v %q", entries, id)
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "playlist.import", string(raw)))
			if job["state"] != "failed" || job["result"] != nil || job["history_id"] != nil {
				t.Fatalf("failed HTTP import returned success or changes: %v", job)
			}
			failure := job["error"].(map[string]any)
			if failure["code"] != "playlist_import_unavailable" {
				t.Fatalf("lost import failure code: %v", job)
			}
		})
	}
}
