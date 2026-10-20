package migrate

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	djsync "github.com/alexgorbatchev/engine-cli/internal/migrate/sync"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	music := filepath.Join(base, "Music")
	if err := os.MkdirAll(music, 0755); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(music, "Artist & Track.mp3")
	if err := os.WriteFile(audio, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(music, "Engine Library")
	library := lib.Library{DatabaseUUID: "test", SchemaVersionMajor: 2, SchemaVersionMinor: 20,
		Songs: []lib.Song{
			{SongID: 1, Path: "../Artist & Track.mp3", Title: "Artist & Track", Filetype: "mp3", Size: 5, SampleRate: 44100, Length: 300, Bpm: 120, Grid: []lib.Marker{{StartPosition: 0, Bpm: 120}}, Cues: []lib.HotCue{{Name: "Intro", Position: 1, Offset: 1, Color: "#ff0000"}}},
			{SongID: 2, Path: "../missing.mp3", Title: "Missing", Filetype: "mp3"},
			{SongID: 3, Path: "../stream.mp3", Title: "Stream", StreamingSource: "tidal"},
		}, Playlists: []lib.Playlist{{PlaylistID: 1, Name: "House", SubPlaylists: []lib.Playlist{{PlaylistID: 2, Name: "Set", Songs: []int{1, 2, 3}}}}}}
	if err := engine.Export(library, source, engine.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	// Native beat data from djtools' songsOriginal SQL fixture at the pinned
	// revision exercises the importer independently of its synthetic exporter.
	encoded, err := os.ReadFile("testdata/beat.hex")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	database, err := engine.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdatePerformanceData(context.Background(), &engine.PerformanceData{TrackID: 1, SampleRate: 44100, BeatDataBlob: blob, HotCues: library.Songs[0].Cues}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return source, audio
}

func TestExportPreservesSourceAudioAndPerformanceData(t *testing.T) {
	source, audio := fixture(t)
	database := filepath.Join(source, "Database2", "m.db")
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target with spaces")
	report, err := Export(context.Background(), Options{Source: source, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Unavailable != 2 || report.Total != 3 || report.Failed != 0 {
		t.Fatalf("report=%+v", report)
	}
	doc, err := rbxml.OpenDocument(report.XML)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Collection.Tracks) != 1 {
		t.Fatalf("collection=%+v", doc.Collection)
	}
	track := doc.Collection.Tracks[0]
	uri, err := url.Parse(track.Location)
	if err != nil {
		t.Fatal(err)
	}
	if uri.Host != "localhost" || strings.HasPrefix(uri.Path, "//") || !strings.HasPrefix(uri.Path, target) {
		t.Fatalf("URI=%s", track.Location)
	}
	data, err := os.ReadFile(uri.Path)
	if err != nil || string(data) != "audio" {
		t.Fatalf("audio=%q err=%v", data, err)
	}
	if track.Tempo == nil || len(*track.Tempo) == 0 || track.PositionMark == nil || len(*track.PositionMark) == 0 {
		t.Fatalf("performance data lost: %+v", track)
	}
	folders := *doc.Playlists.Node.Nodes
	if folders[0].Name != "House" || (*folders[0].Nodes)[0].Entries != 1 {
		t.Fatalf("playlists=%+v", folders)
	}
	after, err := os.ReadFile(database)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source database changed: %v", err)
	}
	if data, err := os.ReadFile(audio); err != nil || string(data) != "audio" {
		t.Fatalf("source audio changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatal("source side effects")
	}
	report, err = Export(context.Background(), Options{Source: source, Target: target})
	if err != nil || report.Existing != 1 || report.Copied != 0 {
		t.Fatalf("resume=%+v err=%v", report, err)
	}
	if err := os.WriteFile(uri.Path, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err = Export(context.Background(), Options{Source: source, Target: target})
	if err != nil || report.Copied != 1 {
		t.Fatalf("changed content trusted: %+v err=%v", report, err)
	}
}

func TestExportValidationAndOutputFailures(t *testing.T) {
	source, _ := fixture(t)
	base := t.TempDir()
	blocked := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(base, "corrupt")
	if err := os.MkdirAll(corrupt, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, ".sync_state.json"), []byte("invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []Options{{}, {Source: filepath.Join(base, "missing"), Target: filepath.Join(base, "invalid")}, {Source: source, Target: filepath.Join(blocked, "target")}, {Source: source, Target: corrupt}} {
		if _, err := Export(context.Background(), opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "invalid")); !os.IsNotExist(err) {
		t.Fatal("invalid source created output")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Export(ctx, Options{Source: source}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	target := filepath.Join(base, "xml-failure")
	if err := os.MkdirAll(filepath.Join(target, "rekordbox.xml"), 0755); err != nil {
		t.Fatal(err)
	}
	if r, err := Export(context.Background(), Options{Source: source, Target: target}); err == nil || r.Copied != 1 {
		t.Fatalf("hidden XML failure: %+v %v", r, err)
	}
}

func TestExportCancellationRetainsCompletedWork(t *testing.T) {
	source, _ := fixture(t)
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []progress.Event
	ctx = progress.WithReporter(ctx, func(e progress.Event) {
		events = append(events, e)
		if e.Phase == "migrate.copy" && e.Percent > 10 {
			cancel()
		}
	})
	report, err := Export(ctx, Options{Source: source})
	if !errors.Is(err, context.Canceled) || report == nil {
		t.Fatalf("cancellation=%+v %v", report, err)
	}
	if _, err := os.Stat(report.XML); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "complete" || e.Percent >= 100 {
			t.Fatal("export emitted terminal progress")
		}
	}
	report, err = Export(context.Background(), Options{Source: source})
	if err != nil || report.Copied+report.Existing != 1 {
		t.Fatalf("resume=%+v err=%v", report, err)
	}
}

func TestExportCopyFailurePublishesOnlyAvailableTracks(t *testing.T) {
	source, audio := fixture(t)
	target := t.TempDir()
	_, mappings := djsync.RemapLibrary(&lib.Library{Songs: []lib.Song{{SongID: 1, Path: audio}}}, target)
	if err := os.MkdirAll(mappings[0].TargetPath, 0755); err != nil {
		t.Fatal(err)
	}
	r, err := Export(context.Background(), Options{Source: source, Target: target})
	if err == nil || r.Failed != 1 || len(r.Failures) != 1 || r.Failures[0].ErrorCode != "operation_failed" {
		t.Fatalf("failure=%+v %v", r, err)
	}
	doc, err := rbxml.OpenDocument(r.XML)
	if err != nil || len(doc.Collection.Tracks) != 0 {
		t.Fatalf("failed track in XML: %+v %v", doc, err)
	}
}

func TestXMLKeepsExactLongURIsAndFiltersRemovedAudio(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("folder with spaces ", 7))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(dir, "track & mix.mp3")
	if err := os.WriteFile(audio, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	library := lib.Library{Songs: []lib.Song{{SongID: 1, Path: audio}, {SongID: 2, Path: filepath.Join(dir, "missing.mp3")}}, Playlists: []lib.Playlist{{Name: "Set", Songs: []int{1, 2}}}}
	path := filepath.Join(t.TempDir(), "rekordbox.xml")
	if err := exportXML(path, library, map[int]bool{1: true, 2: true}); err != nil {
		t.Fatal(err)
	}
	doc, err := rbxml.OpenDocument(path)
	if err != nil || len(doc.Collection.Tracks) != 1 {
		t.Fatalf("document=%+v err=%v", doc, err)
	}
	uri, err := url.Parse(doc.Collection.Tracks[0].Location)
	if err != nil || uri.Path != audio {
		t.Fatalf("shortened URI=%v err=%v", uri, err)
	}
}

func TestStateSaveFailureIsReturnedWithCopiedWork(t *testing.T) {
	source, _ := fixture(t)
	target := t.TempDir()
	var blocked bool
	ctx := progress.WithReporter(context.Background(), func(e progress.Event) {
		if e.Phase == "migrate.copy" && !blocked {
			blocked = true
			if err := os.Mkdir(filepath.Join(target, ".sync_state.json"), 0755); err != nil {
				t.Fatal(err)
			}
		}
	})
	r, err := Export(ctx, Options{Source: source, Target: target})
	if err == nil || r == nil || r.Copied != 1 {
		t.Fatalf("lost state failure: %+v %v", r, err)
	}
	if _, err := os.Stat(r.XML); err != nil {
		t.Fatal(err)
	}
}

func TestExportRejectsLegacyOrDirectoryDatabase(t *testing.T) {
	for _, directory := range []bool{false, true} {
		source := t.TempDir()
		if directory {
			if err := os.MkdirAll(filepath.Join(source, "Database2", "m.db"), 0755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(source, "m.db"), []byte("legacy"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Export(context.Background(), Options{Source: source, Target: filepath.Join(t.TempDir(), "output")}); err == nil {
			t.Fatal("accepted invalid current library")
		}
	}
}
