package api

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/rekordbox"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func apiRekordboxFixture(t *testing.T) (*Server, string, rekordbox.Options) {
	t.Helper()
	s, root := testAPI(t)
	path := filepath.Join(t.TempDir(), "source.xml")
	media := (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(filepath.Join(root, "missing.wav"))}).String()
	data := `<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="` + media + `"/></COLLECTION></DJ_PLAYLISTS>`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return s, root, rekordbox.Options{Path: path, Name: "Imported Rekordbox", MissingMedia: "skip", DryRun: true}
}

func runRekordbox(t *testing.T, s *Server, opts rekordbox.Options) (any, string, error) {
	t.Helper()
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s.RunOperation(t.Context(), "migrate.import", raw)
}

func TestRekordboxImportProtectionAndConsentPrecedeArtifacts(t *testing.T) {
	s, root, opts := apiRekordboxFixture(t)
	s.config.NoBackup = false
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	result, id, err := runRekordbox(t, s, opts)
	if err != nil || id != "" || !result.(*rekordbox.Review).CanApply {
		t.Fatalf("protected preview=%#v history=%q error=%v", result, id, err)
	}
	token := result.(*rekordbox.Review).PlanToken
	opts.DryRun = false
	for _, review := range []struct{ token, code string }{{"not reviewed", "rekordbox_import_stale"}, {token, "engine_desktop_running"}} {
		opts.PlanToken = review.token
		result, id, err = runRekordbox(t, s, opts)
		if result != nil || id != "" || progress.ErrorCode(err) != review.code {
			t.Fatalf("rejected apply=%#v history=%q error=%v; expected %s", result, id, err, review.code)
		}
	}
	for _, path := range []string{filepath.Join(root, ".engine-cli"), filepath.Join(root, "Database2", "imports.db")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("protected review/rejection wrote %s: %v", path, err)
		}
	}
}

func TestRekordboxImportAPIUsesOneSnapshotHistoryAndUndo(t *testing.T) {
	s, root, opts := apiRekordboxFixture(t)
	s.config.NoBackup = false
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	preview := runAPIJob(t, s, "migrate.import", string(raw))
	if preview["history_id"] != nil {
		t.Fatal("preview created history")
	}
	opts.DryRun, opts.PlanToken = false, preview["result"].(map[string]any)["plan_token"].(string)
	raw, err = json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "migrate.import", string(raw))
	result := job["result"].(map[string]any)
	if result["root_id"].(float64) <= 0 || result["created_count"] != float64(0) || result["skipped_count"] != float64(1) {
		t.Fatalf("incorrect import receipt: %+v", job)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v error=%v", snapshots, err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 1 || entries[0].ID != job["history_id"] {
		t.Fatalf("history=%+v error=%v", entries, err)
	}
	undo, err := json.Marshal(map[string]any{"id": job["history_id"]})
	if err != nil {
		t.Fatal(err)
	}
	runAPIJob(t, s, "library.undo", string(undo))
	archive, err := importsource.Read(t.Context(), root)
	if err != nil || len(archive.Documents) != 0 {
		t.Fatalf("undo retained imported source: %+v %v", archive, err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM Playlist`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("undo changed existing hierarchy: %d %v", count, err)
	}
}

func TestRekordboxImportFailedProducersReturnTrueNil(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "preview", false: "apply"}[dryRun], func(t *testing.T) {
			s, root, opts := apiRekordboxFixture(t)
			if dryRun {
				if err := os.Remove(opts.Path); err != nil {
					t.Fatal(err)
				}
			} else {
				result, _, err := runRekordbox(t, s, opts)
				if err != nil {
					t.Fatal(err)
				}
				opts.DryRun, opts.PlanToken = false, result.(*rekordbox.Review).PlanToken
				s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
					if err := os.Remove(opts.Path); err != nil {
						return nil, err
					}
					return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
				}
			}
			result, id, err := runRekordbox(t, s, opts)
			if result != nil || err == nil {
				t.Fatalf("failed operation boxed a result: %#v history=%q error=%v", result, id, err)
			}
			entries, err := history.List(root)
			if err != nil || dryRun && len(entries) != 0 || !dryRun && (len(entries) != 1 || entries[0].ID != id || entries[0].State != "failed") {
				t.Fatalf("failure history=%+v id=%q error=%v", entries, id, err)
			}
			raw, err := json.Marshal(opts)
			if err != nil {
				t.Fatal(err)
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "migrate.import", string(raw)))
			if job["state"] != "failed" || job["result"] != nil {
				t.Fatalf("failed HTTP job has result: %+v", job)
			}
			data, err := json.Marshal(job)
			if err != nil || strings.Contains(string(data), opts.Path) {
				t.Fatalf("raw source diagnostics escaped failure boundary: %s %v", data, err)
			}
		})
	}
}
