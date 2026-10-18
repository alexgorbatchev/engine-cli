package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nateranda/djtools/lib"
)

func writeAudio(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInspectionAndSourceResolution(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(filepath.Join(dir))
	t.Setenv("HOME", filepath.Join(dir, "home"))
	source := filepath.Join(dir, "home", "track.mp3")
	writeAudio(t, source, []byte("audio"))
	dst := filepath.Join(dir, "dst.mp3")
	mapping := FileMapping{SourcePath: "track.mp3", TargetPath: dst}
	status, err := InspectTrack(context.Background(), mapping)
	if err != nil || status != StatusPending {
		t.Fatalf("status=%s err=%v", status, err)
	}
	if _, _, err := NewCopier().CopyFile(context.Background(), mapping.SourcePath, dst, nil); err != nil {
		t.Fatal(err)
	}
	status, err = InspectTrack(context.Background(), mapping)
	if err != nil || status != StatusSkipped {
		t.Fatalf("status=%s err=%v", status, err)
	}
	for _, tc := range []struct {
		name    string
		mapping FileMapping
		want    Status
		wantErr bool
	}{
		{"missing", FileMapping{SourcePath: filepath.Join(dir, "missing")}, StatusUnavailable, false},
		{"streaming", FileMapping{IsStreaming: true}, StatusUnavailable, false},
		{"directory-source", FileMapping{SourcePath: dir}, StatusFailed, true},
		{"directory-target", FileMapping{SourcePath: source, TargetPath: dir}, StatusFailed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InspectTrack(context.Background(), tc.mapping)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("status=%s error=%v", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectTrack(ctx, mapping); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := fileDigest(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := fileDigest(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing digest accepted")
	}
	t.Setenv("HOME", "")
	if _, _, err := resolveSource("unknown-relative.mp3"); err == nil {
		t.Fatal("missing home accepted")
	}
}

func TestCopyCancellationMutationAndErrors(t *testing.T) {
	for _, scenario := range []string{"cancel-progress", "change-source", "destination-directory", "blocked-parent", "missing-source"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "target")
			writeAudio(t, src, []byte("audio"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var progress ProgressCallback
			switch scenario {
			case "cancel-progress":
				progress = func(_, _ int64) { cancel() }
			case "change-source":
				progress = func(_, _ int64) {
					if err := os.Chtimes(src, time.Now(), time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
			case "destination-directory":
				if err := os.Mkdir(dst, 0755); err != nil {
					t.Fatal(err)
				}
			case "blocked-parent":
				writeAudio(t, dst, []byte("block"))
				dst = filepath.Join(dst, "track")
			case "missing-source":
				src = filepath.Join(dir, "missing")
			}
			copied, skipped, err := NewCopier().CopyFile(ctx, src, dst, progress)
			if err == nil || copied || skipped {
				t.Fatalf("copied=%v skipped=%v error=%v", copied, skipped, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".audio-") {
					t.Fatal("temporary audio leaked")
				}
			}
		})
	}
}

func TestConcurrentCopiesUseIndependentTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	writeAudio(t, src, []byte(strings.Repeat("audio", 1000)))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, _, err := NewCopier().CopyFile(context.Background(), src, dst, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	a, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("concurrent copies corrupted destination")
	}
}

func TestAtomicStatePersistenceAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	state, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	state.SetTrackError("src", errors.New("copy failed"))
	if state.GetTrackStatus("src").ErrorMsg == "" {
		t.Fatal("error not stored")
	}
	state.SetTrackError("src", nil)
	state.SetTrackStatus("src", StatusPending, "dst", 5)
	if state.GetTrackStatus("src").ErrorMsg != "" {
		t.Fatal("stale error not cleared")
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			state.SetTrackStatus(fmt.Sprint(i), StatusSynced, "dst", 5)
			if err := state.Save(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if loaded.GetTrackStatus(fmt.Sprint(i)).Status != StatusSynced {
			t.Fatal("concurrent save lost track")
		}
	}
	for _, data := range []string{"{}", "null", `{"tracks":null}`} {
		writeAudio(t, path, []byte(data))
		loaded, err := LoadState(path)
		if err != nil {
			t.Fatal(err)
		}
		loaded.SetTrackStatus("new", StatusPending, "", 0)
	}
	writeAudio(t, path, []byte("invalid JSON"))
	if _, err := LoadState(path); err == nil {
		t.Fatal("corrupt JSON accepted")
	}
	if _, err := LoadState(dir); err == nil {
		t.Fatal("directory accepted as state")
	}
	if err := NewState("").Save(); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, path, []byte("block"))
	if err := NewState(filepath.Join(path, "state")).Save(); err == nil {
		t.Fatal("blocked state path accepted")
	}
	if err := WriteAtomic(dir, []byte("x")); err == nil {
		t.Fatal("directory replaced by metadata")
	}
}

func TestRemappingStabilityAndUnicode(t *testing.T) {
	source := "/Music/" + strings.Repeat("long/", 60) + strings.Repeat("音", 80) + ".mp3"
	library := lib.Library{Songs: []lib.Song{{SongID: 1, Path: source}, {SongID: 2, Path: "/other/track.mp3", URI: "tidal:1"}}}
	remapped, mappings := RemapLibrary(&library, "/target")
	if !utf8.ValidString(mappings[0].TargetPath) || len(mappings[0].TargetPath) > 255 {
		t.Fatal("invalid shortened path")
	}
	if !mappings[1].IsStreaming || library.Songs[0].Path == remapped.Songs[0].Path {
		t.Fatal("streaming or clone behavior incorrect")
	}
	library.Songs[0], library.Songs[1] = library.Songs[1], library.Songs[0]
	_, reordered := RemapLibrary(&library, "/target")
	if mappings[0].TargetPath != reordered[1].TargetPath {
		t.Fatal("path changed with ordering")
	}
	if got := computeTargetPath("/track.mp3", "/"+strings.Repeat("a", 250)); !strings.HasSuffix(got, ".mp3") {
		t.Fatal("extension lost")
	}
}
