package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
)

func backupReport(t *testing.T, s *Server) backup.Report {
	t.Helper()
	raw, err := json.Marshal(runAPIJob(t, s, "library.backups", `{}`)["result"])
	if err != nil {
		t.Fatal(err)
	}
	var report backup.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("result is not a backups report: %s", raw)
	}
	return report
}

func restoreReview(t *testing.T, job map[string]any) backup.RestoreReview {
	t.Helper()
	raw, err := json.Marshal(job["result"])
	if err != nil {
		t.Fatal(err)
	}
	var review backup.RestoreReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatalf("result is not a restore review: %s", raw)
	}
	return review
}

func TestAPILabelledBackupsListNewestFirstWithAutomaticBackupsBeforeTheirChange(t *testing.T) {
	s, _ := testAPI(t)
	first := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"1","genre":"House"}`))
	labelled := runAPIJob(t, s, "library.backup", `{"label":"Before cleanup","reason":"cleanup session"}`)
	created := labelled["result"].(map[string]any)
	if created["label"] != "Before cleanup" || created["kind"] != backup.KindManual || created["history_head"] != first {
		t.Fatalf("created=%+v", created)
	}
	// Automatic backups are taken before each write when backups are on.
	s.config.NoBackup = false
	time.Sleep(1100 * time.Millisecond)
	second := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"1","genre":"Techno"}`))
	report := backupReport(t, s)
	if len(report.Backups) != 2 || report.KeptAudio.Files != 0 {
		t.Fatalf("report=%+v", report)
	}
	automatic, manual := report.Backups[0], report.Backups[1]
	if automatic.Kind != backup.KindAutomatic || automatic.Precedes != second || !automatic.Readable {
		t.Fatalf("automatic=%+v want precedes %s", automatic, second)
	}
	if manual.Label != "Before cleanup" || manual.Reason != "cleanup session" || manual.Precedes != "" || !automatic.Timestamp.After(manual.Timestamp) {
		t.Fatalf("manual=%+v", manual)
	}
	for _, body := range []string{`{"label":"bad\nlabel"}`, `{"label":"` + strings.Repeat("x", 121) + `"}`} {
		if w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.backup", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid label accepted: %d %s", w.Code, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(manual.Path, "backup.json")); err != nil {
		t.Fatalf("backup record missing: %v", err)
	}
}

func TestAPIRestoreReviewListsWhatItRollsBackAndAppliesItsPlan(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.MkdirAll(filepath.Join(lib, "Artwork"), 0o700); err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "library.backup", `{"label":"Before cleanup"}`)
	path := job["result"].(map[string]any)["path"].(string)
	time.Sleep(1100 * time.Millisecond)
	later := historyIdentity(t, runAPIJob(t, s, "track.update", `{"identifier":"1","genre":"House"}`))
	if err := os.WriteFile(filepath.Join(lib, "Artwork", "newer.jpg"), []byte("art"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"path": path, "dry_run": true})
	review := restoreReview(t, runAPIJob(t, s, "library.restore", string(body)))
	if review.Backup.Label != "Before cleanup" || review.Contents.Databases == 0 || !review.AudioUnchanged || !review.Undoable || review.PlanToken == "" {
		t.Fatalf("review=%+v", review)
	}
	if len(review.RollsBack) != 1 || review.RollsBack[0].ID != later || review.RollsBack[0].Summary == nil || review.KeepsArtwork != 1 {
		t.Fatalf("rolls back=%+v keeps=%d", review.RollsBack, review.KeepsArtwork)
	}
	if trackGenre(t, lib, 1) != "House" {
		t.Fatal("a restore review changed the library")
	}
	// Another write after the review makes the plan stale; nothing is restored.
	runAPIJob(t, s, "track.update", `{"identifier":"1","comment":"later"}`)
	stale, _ := json.Marshal(map[string]any{"path": path, "plan_token": review.PlanToken})
	failed := awaitAPIJob(t, s, submitAPIJob(t, s, "library.restore", string(stale)))
	if failed["state"] != "failed" || failed["error"].(map[string]any)["code"] != backup.RestorePlanStaleCode {
		t.Fatalf("stale restore=%+v", failed)
	}
	if trackGenre(t, lib, 1) != "House" {
		t.Fatal("a stale restore changed the library")
	}
	fresh := restoreReview(t, runAPIJob(t, s, "library.restore", string(body)))
	apply, _ := json.Marshal(map[string]any{"path": path, "plan_token": fresh.PlanToken})
	restored := runAPIJob(t, s, "library.restore", string(apply))
	if trackGenre(t, lib, 1) != "" || historyIdentity(t, restored) == "" {
		t.Fatalf("restore did not apply: %q %+v", trackGenre(t, lib, 1), restored)
	}
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.restore", `{"path":"x","dry_run":true,"plan_token":"t"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("dry run accepted a plan token: %d", w.Code)
	}
}

func TestAPIRestoreReviewRefusesAnUnreadableBackup(t *testing.T) {
	s, _ := testAPI(t)
	empty := t.TempDir()
	body, _ := json.Marshal(map[string]any{"path": empty, "dry_run": true})
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "library.restore", string(body)))
	if job["state"] != "failed" || job["error"].(map[string]any)["code"] != "backup_unavailable" {
		t.Fatalf("unreadable review=%+v", job)
	}
	missing, _ := json.Marshal(map[string]any{"path": filepath.Join(empty, "missing"), "dry_run": true})
	job = awaitAPIJob(t, s, submitAPIJob(t, s, "library.restore", string(missing)))
	if job["state"] != "failed" || job["error"].(map[string]any)["code"] != "backup_unavailable" {
		t.Fatalf("missing review=%+v", job)
	}
}
