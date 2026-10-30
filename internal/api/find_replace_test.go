package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

func TestFindReplaceBudgetRejectsBeforeSafetyEnrollment(t *testing.T) {
	s, root := testAPI(t)
	s.config.NoBackup = false
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before := strings.Repeat("x", 2048)
	if _, err := d.Exec("UPDATE Track SET title=? WHERE id=1", before); err != nil {
		t.Fatal(err)
	}
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{}}, nil
	}
	for _, dry := range []bool{true, false} {
		opts := track.FindReplaceOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Find: "^(.*)$", Replacement: new(strings.Repeat("$1", 1024)), Mode: "regex", DryRun: dry, PlanToken: "old"}
		raw, err := json.Marshal(opts)
		if err != nil {
			t.Fatal(err)
		}
		_, id, err := s.RunOperation(t.Context(), "track.batch.find-replace", raw)
		if progress.ErrorCode(err) != "metadata_transform_too_large" || id != "" {
			t.Fatalf("budget rejection: id=%q err=%v", id, err)
		}
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("history=%+v err=%v", entries, err)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
	var after string
	if err := d.QueryRow("SELECT title FROM Track WHERE id=1").Scan(&after); err != nil || after != before || checks != 0 {
		t.Fatalf("preflight side effects: checks=%d err=%v", checks, err)
	}
}

func TestFindReplaceSnapshotUndoAndProtection(t *testing.T) {
	s, root := testAPI(t)
	s.config.NoBackup = false
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title='Mix One',artist='DJ First' WHERE id=1; INSERT INTO Track(id,title,artist,path) VALUES(2,'Mix Two','DJ Second','two.mp3')"); err != nil {
		t.Fatal(err)
	}
	options := track.FindReplaceOptions{Identifiers: []string{"1", "2"}, Fields: []string{"title"}, Find: "Mix", Replacement: new("Set"), DryRun: true}
	run := func() (any, string, error) {
		raw, _ := json.Marshal(options)
		return s.RunOperation(context.Background(), "track.batch.find-replace", raw)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	result, id, err := run()
	if err != nil || id != "" {
		t.Fatalf("preview=%+v id=%s err=%v", result, id, err)
	}
	options.PlanToken = result.(*track.MetadataTransformPlan).PlanToken
	options.DryRun = false
	_, _, err = run()
	if progress.ErrorCode(err) != "engine_desktop_running" {
		t.Fatalf("protection=%v", err)
	}
	options.PlanToken = "stale"
	_, _, err = run()
	if progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("stale=%v", err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected write history=%+v err=%v", entries, err)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("rejected write snapshots=%+v err=%v", snapshots, err)
	}
	s.config.CheckSystem = nil
	options.DryRun = true
	result, _, err = run()
	if err != nil {
		t.Fatal(err)
	}
	options.DryRun = false
	options.PlanToken = result.(*track.MetadataTransformPlan).PlanToken
	result, id, err = run()
	if err != nil || id == "" || len(result.(*track.MetadataTransformResult).TrackIDs) != 2 {
		t.Fatalf("apply=%+v id=%s err=%v", result, id, err)
	}
	entries, err = history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("history=%+v err=%v", entries, err)
	}
	snapshots, err = backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
	for _, id := range []int64{1, 2} {
		tr, err := db.GetTrackByID(context.Background(), d, id)
		if err != nil || !strings.HasPrefix(tr.Title.String, "Set") {
			t.Fatalf("track=%+v err=%v", tr, err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"id": id})
	if _, _, err := s.RunOperation(context.Background(), "library.undo", raw); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		tr, err := db.GetTrackByID(context.Background(), d, id)
		if err != nil || !strings.HasPrefix(tr.Title.String, "Mix") {
			t.Fatalf("undo track=%+v err=%v", tr, err)
		}
	}
}
