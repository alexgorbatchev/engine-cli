package track

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// outdatedTool puts a managed tool reporting version first on PATH. It
// returns a marker the tool creates when it runs for anything but --version.
func outdatedTool(t *testing.T, name, version string) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tools := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo " + version + "; exit 0; fi\n: > " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// Collection files meet the same tag-track requirement as staged and
// unmanaged files: without an injected runner, an outdated tag-track is
// refused before it runs instead of failing on flags it does not know.
func TestUpdateFileTagsRequiresSupportedTagTrack(t *testing.T) {
	marker := outdatedTool(t, "tag-track", "1.1.0")
	d, lib, _ := createTestDB(t)
	if err := os.WriteFile(filepath.Join(lib, "original.mp3"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,artist,path) VALUES(1,'Old','Artist','original.mp3')`); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateFileTags(context.Background(), d, lib, "1", FileTagOptions{Options: acquire.TagOptions{Title: new("New")}})
	if code := progress.ErrorCode(err); code != "tool_outdated" {
		t.Fatalf("outdated tag-track coded %q: %v", code, err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("outdated tag-track ran")
	}
}

// The default upgrade runner runs Fetch Track off PATH, so an upgrade checks
// its release after validating the request and refuses an outdated one before
// any download. An injected runner stands in for the tool and is not checked.
func TestUpgradeTrackRequiresSupportedFetchTrack(t *testing.T) {
	marker := outdatedTool(t, "fetch-track", "1.11.0")
	database, libDir, _ := createTestDB(t)
	if err := os.WriteFile(filepath.Join(libDir, "original.mp3"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Track (id,path,filename,title,artist) VALUES (1,'original.mp3','original.mp3','Title','Artist')`); err != nil {
		t.Fatal(err)
	}
	_, err := UpgradeTrack(context.Background(), database, libDir, "1", UpgradeOptions{})
	if code := progress.ErrorCode(err); code != "tool_outdated" {
		t.Fatalf("outdated fetch-track coded %q: %v", code, err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("outdated fetch-track ran an acquisition")
	}
	// A request that fails validation reports that instead.
	_, err = UpgradeTrack(context.Background(), database, libDir, "999", UpgradeOptions{})
	if code := progress.ErrorCode(err); code == "tool_outdated" {
		t.Fatalf("missing track reported %v", err)
	}
	ran := false
	runner := func(context.Context, string, ...string) ([]byte, error) { ran = true; return nil, context.Canceled }
	_, err = UpgradeTrack(context.Background(), database, libDir, "1", UpgradeOptions{Runner: runner, Tools: acquire.Client{Runner: runner}})
	if !ran || progress.ErrorCode(err) == "tool_outdated" {
		t.Fatalf("injected runner ran=%t error=%v", ran, err)
	}
}
