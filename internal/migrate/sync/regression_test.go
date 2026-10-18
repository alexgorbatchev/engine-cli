package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	djsync "github.com/dj/engine-cli/internal/migrate/sync"
	"github.com/nateranda/djtools/lib"
)

func TestRemapDistinctSources(t *testing.T) {
	library := lib.Library{Songs: []lib.Song{{Path: "/A/song.mp3"}, {Path: "/B/song.mp3"}}}
	_, mappings := djsync.RemapLibrary(&library, t.TempDir())
	if mappings[0].TargetPath == mappings[1].TargetPath {
		t.Fatal("distinct tracks share a destination")
	}
}

func TestCopyReplacesDifferentContentOfSameSize(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	for path, data := range map[string]string{src: "new", dst: "old"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	copied, skipped, err := djsync.NewCopier().CopyFile(context.Background(), src, dst, nil)
	if err != nil || !copied || skipped {
		t.Fatalf("copied=%v skipped=%v error=%v", copied, skipped, err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "new" {
		t.Fatalf("destination=%q error=%v", data, err)
	}
}
