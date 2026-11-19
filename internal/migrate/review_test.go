package migrate

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
	_ "modernc.org/sqlite"
)

// scopedFixture has a relative and an absolute track in Set, a missing track and
// a track without beat data in Other, a streaming track in no playlist, and the
// folder House holding both playlists.
func scopedFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	music := filepath.Join(base, "Music")
	outside := filepath.Join(t.TempDir(), "Elsewhere")
	for _, dir := range []string{music, outside} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(music, "relative.mp3"): "relative", filepath.Join(outside, "absolute.mp3"): "absolute audio", filepath.Join(music, "unanalyzed.mp3"): "unanalyzed"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(music, "Engine Library")
	grid := []lib.Marker{{StartPosition: 0, Bpm: 120}}
	library := lib.Library{DatabaseUUID: "scoped", SchemaVersionMajor: 2, SchemaVersionMinor: 20,
		Songs: []lib.Song{
			{SongID: 1, Path: "../relative.mp3", Title: "Relative", Artist: "Ana", Filetype: "mp3", SampleRate: 44100, Length: 300, Grid: grid},
			{SongID: 2, Path: "../absolute-placeholder.mp3", Title: "Absolute", Artist: "Ben", Filetype: "mp3", SampleRate: 44100, Length: 300, Grid: grid},
			{SongID: 3, Path: "../missing.mp3", Title: "Missing", Artist: "Cy", Filetype: "mp3", SampleRate: 44100, Length: 300, Grid: grid},
			{SongID: 4, Path: "../unanalyzed.mp3", Title: "Unanalyzed", Artist: "Di", Filetype: "mp3", SampleRate: 44100, Length: 300, Grid: grid},
			{SongID: 5, Path: "../stream.mp3", Title: "Stream", Artist: "Ed", StreamingSource: "tidal", SampleRate: 44100, Length: 300, Grid: grid},
		},
		Playlists: []lib.Playlist{{PlaylistID: 1, Name: "House", SubPlaylists: []lib.Playlist{{PlaylistID: 2, Name: "Set", Songs: []int{1, 2}}, {PlaylistID: 3, Name: "Other", Songs: []int{3, 4, 1}}}}}}
	// Desktop records each track's origin; memberships resolve through it.
	for i := range library.Songs {
		library.Songs[i].OriginDatabaseUUID, library.Songs[i].OriginTrackID = library.DatabaseUUID, library.Songs[i].SongID
	}
	if err := engine.Export(library, source, engine.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile("testdata/beat.hex")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	d, err := sql.Open("sqlite", filepath.Join(source, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"UPDATE PerformanceData SET beatData=? WHERE trackId IN (1,2,3,5)", []any{blob}},
		{"UPDATE PerformanceData SET beatData=NULL WHERE trackId=4", nil},
		{"UPDATE Track SET path=? WHERE id=2", []any{filepath.Join(outside, "absolute.mp3")}},
	} {
		if _, err := d.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return source, outside
}

func entryStatuses(plan *Plan) string {
	parts := make([]string, len(plan.Entries))
	for i, entry := range plan.Entries {
		parts[i] = fmt.Sprintf("%s:%s", entry.Title, entry.Status)
	}
	return strings.Join(parts, " ")
}

func TestReviewListsEveryTrackAsExportedOrLeftOut(t *testing.T) {
	source, _ := scopedFixture(t)
	target := filepath.Join(t.TempDir(), "DJ_USB", "Export")
	plan, err := Review(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStatuses(plan); got != "Relative:ready Absolute:ready Missing:unavailable Unanalyzed:no_beat_data Stream:streaming" {
		t.Fatalf("entries=%s", got)
	}
	if plan.TrackCount != 5 || plan.ExportCount != 2 || plan.LeftOutCount != 3 || plan.PlaylistCount != 3 || plan.ExportBytes != int64(len("relative")+len("absolute audio")) || plan.CopyBytes != plan.ExportBytes {
		t.Fatalf("counts=%+v", plan)
	}
	if plan.FreeBytes == nil || *plan.FreeBytes <= 0 || !plan.CanApply || plan.XMLExists || plan.PlanToken == "" {
		t.Fatalf("plan=%+v", plan)
	}
	if house := plan.Playlists[0]; house.Title != "House" || !house.Included || len(house.Children) != 2 || house.Children[1].TrackCount != 3 {
		t.Fatalf("tree=%+v", plan.Playlists)
	}
	if entry := plan.Entries[0]; entry.TrackID == 0 || entry.Artist != "Ana" || entry.Size != int64(len("relative")) {
		t.Fatalf("entry=%+v", entry)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("review created the destination")
	}
}

func TestReviewScopesPlaylistsAndTrackLists(t *testing.T) {
	source, _ := scopedFixture(t)
	target := t.TempDir()
	plan, err := Review(context.Background(), Options{Source: source, Target: target, Playlists: []string{"3"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStatuses(plan); got != "Missing:unavailable Unanalyzed:no_beat_data Relative:ready" || plan.PlaylistCount != 1 || plan.Playlists[0].Included || !plan.Playlists[0].Children[1].Included {
		t.Fatalf("playlist scope=%s %+v", got, plan.Playlists)
	}
	whole, err := Review(context.Background(), Options{Source: source, Target: target})
	if err != nil || whole.PlanToken == plan.PlanToken {
		t.Fatalf("scopes share a token: %v", err)
	}
	plan, err = Review(context.Background(), Options{Source: source, Target: target, Tracks: []string{"2", "1", "2"}, Name: " Peak Time selection "})
	if err != nil {
		t.Fatal(err)
	}
	if got := entryStatuses(plan); got != "Absolute:ready Relative:ready" || plan.Name != "Peak Time selection" || plan.PlaylistCount != 1 {
		t.Fatalf("track scope=%s %+v", got, plan)
	}
	for _, opts := range []Options{
		{Source: source, Target: target, Tracks: []string{"1"}},
		{Source: source, Target: target, Tracks: []string{"99"}, Name: "x"},
		{Source: source, Target: target, Playlists: []string{"9"}},
		{Source: source, Target: target, Playlists: []string{"1"}, Tracks: []string{"1"}, Name: "x"},
		{Source: source, Target: target, Name: "x"},
	} {
		if _, err := Review(context.Background(), opts); progress.ErrorCode(err) != scopeCode {
			t.Errorf("accepted scope %+v: %v", opts, err)
		}
	}
}

func TestExportAppliesOnlyTheReviewedScope(t *testing.T) {
	source, outside := scopedFixture(t)
	target := t.TempDir()
	opts := Options{Source: source, Target: target, Playlists: []string{"2"}}
	plan, err := Review(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	report, err := Export(context.Background(), opts)
	if err != nil || report.Exported != 2 || report.Copied != 2 || report.Total != 2 || len(report.LeftOut) != 0 || report.Playlists != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	doc, err := rbxml.OpenDocument(report.XML)
	if err != nil {
		t.Fatal(err)
	}
	house := (*doc.Playlists.Node.Nodes)[0]
	if house.Name != "House" || len(*house.Nodes) != 1 || (*house.Nodes)[0].Name != "Set" || (*house.Nodes)[0].Entries != 2 || len(doc.Collection.Tracks) != 2 {
		t.Fatalf("xml=%+v %+v", house, doc.Collection.Tracks)
	}
	// The absolute stored path was read as stored, not joined onto the library.
	if data, err := os.ReadFile(filepath.Join(outside, "absolute.mp3")); err != nil || string(data) != "absolute audio" {
		t.Fatalf("source changed: %v", err)
	}
	plan, err = Review(context.Background(), opts)
	if err != nil || plan.PresentCount != 2 || plan.CopyBytes != 0 || !plan.XMLExists || plan.PlanToken != opts.PlanToken {
		t.Fatalf("re-review=%+v %v", plan, err)
	}
	selection := Options{Source: source, Target: t.TempDir(), Tracks: []string{"4", "1"}, Name: "Picks"}
	report, err = Export(context.Background(), selection)
	if err != nil || report.Exported != 1 || len(report.LeftOut) != 1 || report.LeftOut[0].Status != StatusNoBeatData {
		t.Fatalf("selection=%+v %v", report, err)
	}
	doc, err = rbxml.OpenDocument(report.XML)
	if err != nil {
		t.Fatal(err)
	}
	if nodes := *doc.Playlists.Node.Nodes; len(nodes) != 1 || nodes[0].Name != "Picks" || nodes[0].Entries != 1 {
		t.Fatalf("selection xml=%+v", nodes)
	}
}

func TestExportRefusesStaleEmptyAndOverfullPlans(t *testing.T) {
	source, outside := scopedFixture(t)
	target := t.TempDir()
	opts := Options{Source: source, Target: target, Playlists: []string{"2"}}
	plan, err := Review(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "absolute.mp3"), []byte("re-encoded audio"), 0644); err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	if _, err := Export(context.Background(), opts); progress.ErrorCode(err) != staleCode {
		t.Fatalf("stale plan applied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "rekordbox.xml")); !os.IsNotExist(err) {
		t.Fatal("stale plan wrote output")
	}
	if _, err := Export(context.Background(), Options{Source: source, Target: target, Tracks: []string{"3", "4"}, Name: "Nothing"}); progress.ErrorCode(err) != nothingCode {
		t.Fatalf("empty export applied: %v", err)
	}
	full := &Plan{Entries: []PlanEntry{{Status: StatusReady, Size: 10}}, XML: filepath.Join(target, "rekordbox.xml")}
	if err := countPlan(full, target); err != nil || !full.CanApply {
		t.Fatalf("plan=%+v %v", full, err)
	}
	free := int64(5)
	if canApply(full.ExportCount, full.CopyBytes, &free) || !canApply(1, 0, &free) || !canApply(1, 99, nil) || canApply(0, 0, nil) {
		t.Fatal("free space does not gate the export")
	}
	if got := existingAncestor(filepath.Join(target, "a", "b")); got != target {
		t.Fatalf("ancestor=%s", got)
	}
}
