package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/testaudio"
)

// guardAPI is a library whose collection holds "Artist - Curated title" at the
// given length, with a fetch tool that downloads that track at 200 seconds.
func guardAPI(t *testing.T, length int) (*Server, string) {
	t.Helper()
	s, lib := testAPI(t)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE Track SET length=? WHERE id=1", length); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
	s.config.ToolRunner = func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffprobe" {
			return testaudio.Metadata(`{"format":{"tags":{"title":"Curated title (Original Mix)"}},"streams":[]}`)(ctx, tool, args...)
		}
		index := slices.Index(args, "--out-dir")
		if index < 0 {
			t.Fatalf("no output directory %s %v", tool, args)
		}
		path := filepath.Join(args[index+1], "download.mp3")
		testaudio.Write(t, path)
		return json.Marshal(map[string]any{
			"status":       "success",
			"output_file":  path,
			"metadata":     map[string]any{"title": "Curated title (Original Mix)", "artist": "Artist"},
			"verification": map[string]any{"duration_seconds": 200.4, "bandwidth_hz": 20000, "bandwidth_rating": "good"},
		})
	}
	for _, op := range s.acquisitionOperations() {
		s.operations[op.Name] = op
	}
	return s, lib
}

func TestFetchHoldsADownloadTheCollectionAlreadyHas(t *testing.T) {
	s, lib := guardAPI(t, 200)
	result, id, err := s.RunOperation(context.Background(), "track.fetch", json.RawMessage(`{"query":"Artist - Curated title","import":true}`))
	if err != nil || id == "" {
		t.Fatalf("fetch %q %v", id, err)
	}
	response := result.(map[string]any)
	withheld := response["import_withheld"].(map[string]any)
	matches := withheld["matches"].([]duplicate.CollectionMatch)
	if withheld["reason"] != "in_collection" || len(matches) != 1 || matches[0].TrackID != 1 || !matches[0].SameLength {
		t.Fatalf("withheld %+v", withheld)
	}
	if _, imported := response["import"]; imported {
		t.Fatal("imported a track the collection already has")
	}
	items, err := staging.New(lib).List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("staged %v %v", items, err)
	}
	entries, err := history.List(lib)
	if err != nil {
		t.Fatal(err)
	}
	entry := entries[slices.IndexFunc(entries, func(e history.Entry) bool { return e.ID == id })]
	// The held download changed no database, so undoing it removes only the staged files.
	if entry.Scope != history.ScopeFiles {
		t.Fatalf("held download recorded as %q", entry.Scope)
	}
}

func TestFetchImportsAnotherEditOrWhenAskedTo(t *testing.T) {
	for name, tc := range map[string]struct {
		length     int
		parameters string
	}{
		"another edit":      {length: 140, parameters: `{"query":"Artist - Curated title","import":true}`},
		"import if owned":   {length: 200, parameters: `{"query":"Artist - Curated title","import":true,"if_owned":"import"}`},
		"stage is explicit": {length: 140, parameters: `{"query":"Artist - Curated title","import":true,"if_owned":"stage"}`},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := guardAPI(t, tc.length)
			result, _, err := s.RunOperation(context.Background(), "track.fetch", json.RawMessage(tc.parameters))
			if err != nil {
				t.Fatal(err)
			}
			report := result.(map[string]any)["import"].(*staging.ImportReport)
			if len(report.Imported) != 1 {
				t.Fatalf("import %+v", report)
			}
		})
	}
}

func TestFetchRefusesAnUnknownIfOwned(t *testing.T) {
	s, _ := guardAPI(t, 200)
	if _, _, err := s.RunOperation(context.Background(), "track.fetch", json.RawMessage(`{"query":"x","import":true,"if_owned":"replace"}`)); err == nil {
		t.Fatal("accepted an unknown if_owned")
	}
}
