package playlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func fileNames(plan *ExportFolderPlan) string {
	names := make([]string, len(plan.Files))
	for i, file := range plan.Files {
		names[i] = fmt.Sprintf("%s<%s>:%d", file.Name, file.RequestedName, file.ExportedCount)
	}
	return strings.Join(names, " ")
}

func TestFolderExportWritesOneFilePerPlaylistUnderFreeNames(t *testing.T) {
	d, root, _ := exportFixture(t)
	ctx := context.Background()
	crates, err := db.CreatePlaylist(ctx, d, "Crates", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	twin, err := db.CreatePlaylist(ctx, d, "Café set", crates, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTrackToPlaylist(ctx, d, twin, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePlaylist(ctx, d, "Empty / odd", 0, false); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "usb")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "Café set.m3u8"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := ExportOptions{Folder: folder}
	plan, err := PreviewFolderExport(ctx, d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileNames(plan); got != "Café set (2).m3u8<Café set.m3u8>:2 Café set (3).m3u8<Café set.m3u8>:1" {
		t.Fatalf("files=%s", got)
	}
	if plan.PathMode != exportRelative || plan.PathModeReason != pathModeSameVolume || !plan.CanApply || plan.FileCount != 2 || plan.ExportedCount != 3 {
		t.Fatalf("plan=%+v", plan)
	}
	if len(plan.Playlists) != 3 || !plan.Playlists[0].Included || plan.Playlists[2].Included || !plan.Playlists[1].Children[0].Included {
		t.Fatalf("tree=%+v", plan.Playlists)
	}
	opts.PlanToken = plan.PlanToken
	result, err := ApplyFolderExport(ctx, d, root, opts)
	if err != nil || result.FileCount != 2 || result.ExportedCount != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(folder, "Café set (2).m3u8"))
	if err != nil || !strings.Contains(string(data), "\n../first.flac\n") || !strings.HasPrefix(string(data), "#EXTM3U\n") {
		t.Fatalf("file=%q err=%v", data, err)
	}
	if kept, err := os.ReadFile(filepath.Join(folder, "Café set.m3u8")); err != nil || string(kept) != "keep" {
		t.Fatal("existing file was replaced")
	}
	// The reviewed names are now taken, so the same plan is stale.
	if _, err := ApplyFolderExport(ctx, d, root, opts); progress.ErrorCode(err) != "playlist_export_stale" {
		t.Fatalf("replayed plan: %v", err)
	}
	empty, err := PreviewFolderExport(ctx, d, root, ExportOptions{Folder: folder, Identifiers: []string{fmt.Sprint(crates + 2)}})
	if err != nil || empty.CanApply || empty.FileCount != 1 || empty.Files[0].Name != "Empty - odd.m3u8" {
		t.Fatalf("empty playlist=%+v err=%v", empty, err)
	}
}

func TestFolderExportTrackListAndExplicitOptions(t *testing.T) {
	d, root, _ := exportFixture(t)
	ctx := context.Background()
	folder := t.TempDir()
	opts := ExportOptions{Folder: folder, Tracks: []string{"1", "2", "1"}, Name: "Peak Time selection", PathMode: exportAbsolute, Encoding: m3uWindows1252, SkipUnavailable: true}
	plan, err := PreviewFolderExport(ctx, d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileNames(plan); got != "Peak Time selection.m3u<Peak Time selection.m3u>:2" || plan.PathMode != exportAbsolute || plan.PathModeReason != "" {
		t.Fatalf("plan=%s %+v", got, plan)
	}
	if entries := plan.Files[0].Entries; entries[0].TrackID != 1 || entries[1].TrackID != 2 {
		t.Fatalf("order=%+v", entries)
	}
	for _, bad := range []ExportOptions{
		{Folder: folder, Identifier: "1"},
		{Folder: folder, Tracks: []string{"1"}},
		{Folder: folder, Name: "x"},
		{Folder: folder, Tracks: []string{"1"}, Identifiers: []string{"1"}, Name: "x"},
		{Folder: folder, Tracks: []string{"99"}, Name: "x"},
		{Folder: folder, Tracks: []string{"x"}, Name: "x"},
		{Folder: folder, Identifiers: []string{"99"}},
		{Folder: folder, Identifiers: []string{"x"}},
		{Folder: filepath.Join(folder, "missing")},
		{Folder: folder, Source: filepath.Join(folder, "no library")},
	} {
		if _, err := PreviewFolderExport(ctx, d, root, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestFolderExportReadsAnotherLibraryAsSource(t *testing.T) {
	_, root, _ := exportFixture(t)
	route, other, _ := exportFixture(t)
	if _, err := db.CreatePlaylist(context.Background(), route, "Only on the route library", 0, false); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewFolderExport(context.Background(), route, other, ExportOptions{Folder: t.TempDir(), Source: root})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != root || len(plan.Playlists) != 1 || plan.Playlists[0].Title != "Café set" {
		t.Fatalf("source plan=%+v", plan)
	}
}

func TestChooseFolderPathModeFallsBackToAbsoluteAcrossVolumes(t *testing.T) {
	_, root, _ := exportFixture(t)
	target := folderTarget{members: []exportMember{{}}}
	target.members[0].Path.Valid, target.members[0].Path.String = true, "first.flac"
	if mode, reason := chooseFolderPathMode(root, root, []folderTarget{target}); mode != exportRelative || reason != pathModeSameVolume {
		t.Fatalf("same volume=%s %s", mode, reason)
	}
	if sameVolume(filepath.Join(root, "first.flac"), filepath.Join(root, "missing")) || sameVolume(filepath.Join(root, "missing"), root) {
		t.Fatal("missing paths matched a volume")
	}
	if got := exportFileBase(" .. "); got != "Playlist" {
		t.Fatalf("blank base=%q", got)
	}
	if got := exportFileBase(strings.Repeat("é", 300)); len([]rune(got)) != 200 {
		t.Fatalf("long base=%d runes", len([]rune(got)))
	}
}
