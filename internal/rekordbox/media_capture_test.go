package rekordbox

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nateranda/djtools/lib"
)

func TestCapturedMediaRejectsNativeChanges(t *testing.T) {
	for _, change := range []string{"replacement", "size", "time", "removed", "missing appeared"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audio")
			if change != "missing appeared" {
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file, err := observeMedia(path)
			if err != nil || file.check() != nil {
				t.Fatalf("unchanged observation=%+v error=%v", file, err)
			}
			switch change {
			case "replacement":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, file.modified, file.modified); err != nil {
					t.Fatal(err)
				}
			case "size", "missing appeared":
				if err := os.WriteFile(path, []byte("new content"), 0600); err != nil {
					t.Fatal(err)
				}
			case "time":
				next := file.modified.Add(time.Second)
				if err := os.Chtimes(path, next, next); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := file.check(); err == nil {
				t.Fatalf("accepted %s after native capture", change)
			}
		})
	}
}

func TestCaptureMediaResolvesNativeRelativePathsAndAliases(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "audio")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	document, err := observeMedia(path)
	if err != nil {
		t.Fatal(err)
	}
	src := source{selectors: []string{"id:1"}, library: lib.Library{Songs: []lib.Song{{Path: alias}}}}
	destination := destinationObservation{tracks: []nativeTrack{{id: 9, path: "audio"}}, review: archiveReview{tracks: map[string]nativeTrack{}}}
	files, err := captureMedia(t.Context(), src, destination, root, document)
	if err != nil || files.check(t.Context()) != nil {
		t.Fatalf("capture=%+v error=%v", files, err)
	}
	entries, err := classifyEntries(src, destination, files)
	if err != nil || len(entries) != 1 || entries[0].Status != statusExisting || entries[0].TrackID != 9 {
		t.Fatalf("native alias was not reused: %+v %v", entries, err)
	}
	if got, err := decodeNewMedia(t.Context(), entries, files); err != nil || len(got) != 0 {
		t.Fatalf("existing audio was decoded: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := captureMedia(ctx, src, destination, root, document); err != context.Canceled {
		t.Fatalf("capture ignored cancellation: %v", err)
	}
	if _, err := observeMedia(root); err == nil {
		t.Fatal("directory accepted as source audio")
	}
}

func TestDecodeNewMediaUsesNativeExtentAndCapturedBytes(t *testing.T) {
	const rate = 48000
	const frames = 12000
	path := filepath.Join(t.TempDir(), "native.wav")
	data := writeNativeWAV(t, path, rate, frames)
	file, err := observeMedia(path)
	if err != nil {
		t.Fatal(err)
	}
	files := mediaObservation{source: map[string]mediaFile{"id:1": file}}
	entries := []Entry{{SourceID: "id:1", Status: statusNew}}
	got, err := decodeNewMedia(t.Context(), entries, files)
	if err != nil || got["id:1"] != (audioExtent{rate: rate, frames: frames, bytes: int64(len(data))}) {
		t.Fatalf("native extent=%+v error=%v", got, err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeNewMedia(t.Context(), entries, files); err == nil {
		t.Fatal("decoded byte-identical replacement using stale media observation")
	}
}

func writeNativeWAV(t *testing.T, path string, rate, frames int) []byte {
	t.Helper()
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(frames*2))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}
