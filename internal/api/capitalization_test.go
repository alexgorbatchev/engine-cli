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

func TestCapitalizationProtectionSnapshotAndUndo(t *testing.T) {
	s, root := testAPI(t)
	s.config.NoBackup = false
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title='straße' WHERE id=1; INSERT INTO Track(id,title,path) VALUES(2,'café','two.mp3')"); err != nil {
		t.Fatal(err)
	}
	opts := track.CapitalizationOptions{Identifiers: []string{"1", "2"}, Fields: []string{"title"}, Recipe: "upper", DryRun: true}
	run := func() (any, string, error) {
		raw, err := json.Marshal(opts)
		if err != nil {
			t.Fatal(err)
		}
		return s.RunOperation(t.Context(), "track.batch.capitalize", raw)
	}
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	result, id, err := run()
	if err != nil || id != "" || checks != 0 {
		t.Fatalf("read-only review=%+v id=%s checks=%d error=%v", result, id, checks, err)
	}
	opts.PlanToken = result.(*track.MetadataTransformPlan).PlanToken
	opts.DryRun = false
	if _, _, err := run(); progress.ErrorCode(err) != "engine_desktop_running" {
		t.Fatalf("unguarded apply: %v", err)
	}
	opts.PlanToken = "wrong"
	if _, _, err := run(); progress.ErrorCode(err) != "metadata_transform_stale" {
		t.Fatalf("stale apply: %v", err)
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected write history=%+v %v", entries, err)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("rejected write snapshots=%+v %v", snapshots, err)
	}
	s.config.CheckSystem = nil
	opts.DryRun = true
	result, _, err = run()
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	opts.PlanToken = result.(*track.MetadataTransformPlan).PlanToken
	result, id, err = run()
	if err != nil || id == "" || len(result.(*track.MetadataTransformResult).TrackIDs) != 2 {
		t.Fatalf("apply=%+v id=%s %v", result, id, err)
	}
	entries, err = history.List(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("history=%+v %v", entries, err)
	}
	snapshots, err = backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v %v", snapshots, err)
	}
	for i, want := range []string{"STRASSE", "CAFÉ"} {
		tr, err := db.GetTrackByID(t.Context(), d, int64(i+1))
		if err != nil || tr.Title.String != want {
			t.Fatalf("stored=%+v %v", tr, err)
		}
	}
	raw, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "library.undo", raw); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"straße", "café"} {
		tr, err := db.GetTrackByID(t.Context(), d, int64(i+1))
		if err != nil || tr.Title.String != want {
			t.Fatalf("undo=%+v %v", tr, err)
		}
	}
}

func TestCapitalizationBudgetPrecedesSafetyEnrollment(t *testing.T) {
	s, root := testAPI(t)
	s.config.NoBackup = false
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("UPDATE Track SET title=? WHERE id=1", strings.Repeat("ΐ", (1<<20)/3)); err != nil {
		t.Fatal(err)
	}
	checks := 0
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		checks++
		return &systemcheck.Report{}, nil
	}
	for _, dry := range []bool{true, false} {
		raw, err := json.Marshal(track.CapitalizationOptions{Identifiers: []string{"1"}, Fields: []string{"title"}, Recipe: "upper", DryRun: dry, PlanToken: "old"})
		if err != nil {
			t.Fatal(err)
		}
		_, id, err := s.RunOperation(t.Context(), "track.batch.capitalize", raw)
		if progress.ErrorCode(err) != "metadata_transform_too_large" || id != "" {
			t.Fatalf("oversized plan id=%s %v", id, err)
		}
	}
	entries, err := history.List(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("history=%+v %v", entries, err)
	}
	snapshots, err := backup.ListBackups(root, "")
	if err != nil || len(snapshots) != 0 || checks != 0 {
		t.Fatalf("snapshots=%+v checks=%d %v", snapshots, checks, err)
	}
}
