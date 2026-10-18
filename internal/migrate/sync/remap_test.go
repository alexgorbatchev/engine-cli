package sync_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/migrate/sync"
	"github.com/nateranda/djtools/lib"
)

func TestRemapLibrary(t *testing.T) {
	sourceLib := lib.Library{
		Songs: []lib.Song{
			{
				SongID: 1,
				Title:  "Track One",
				Artist: "Artist A",
				Path:   "/Volumes/PRIME-128GB/Music/Folder/Track1.mp3",
				Size:   5000,
			},
			{
				SongID: 2,
				Title:  "Track Two",
				Artist: "Artist B",
				Path:   "/library/Music/Track2.wav",
				Size:   10000,
			},
		},
		Playlists: []lib.Playlist{
			{
				PlaylistID: 10,
				Name:       "House Set",
				Songs:      []int{1, 2},
			},
		},
	}

	targetDriveDir := "/Volumes/ENGINE32GB"
	remappedLib, mappings := sync.RemapLibrary(&sourceLib, targetDriveDir)

	if len(remappedLib.Songs) != 2 {
		t.Fatalf("expected 2 remapped songs, got %d", len(remappedLib.Songs))
	}

	expectedPath1 := remappedLib.Songs[0].Path
	if filepath.Dir(expectedPath1) != "/Volumes/ENGINE32GB/Contents/Music/Folder" || !strings.HasSuffix(expectedPath1, ".mp3") {
		t.Errorf("unexpected song 1 path %q", expectedPath1)
	}

	expectedPath2 := remappedLib.Songs[1].Path
	if filepath.Dir(expectedPath2) != "/Volumes/ENGINE32GB/Contents/Music" || !strings.HasSuffix(expectedPath2, ".wav") {
		t.Errorf("unexpected song 2 path %q", expectedPath2)
	}

	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	if mappings[0].SourcePath != "/Volumes/PRIME-128GB/Music/Folder/Track1.mp3" || mappings[0].TargetPath != expectedPath1 {
		t.Errorf("unexpected mapping 0: %+v", mappings[0])
	}
}

func TestRemapLibrary_LongPathTruncation(t *testing.T) {
	longFolder := strings.Repeat("VeryLongSubfolderName/", 10)
	longFilename := strings.Repeat("A", 150) + ".mp3"
	longSrcPath := "/library/source/Contents/Music/" + longFolder + longFilename

	sourceLib := lib.Library{
		Songs: []lib.Song{
			{
				SongID: 100,
				Title:  "Long Track",
				Path:   longSrcPath,
				Size:   12345,
			},
		},
	}

	targetDriveDir := "/library/destination/rekordbox"
	remappedLib, _ := sync.RemapLibrary(&sourceLib, targetDriveDir)

	remappedPath := remappedLib.Songs[0].Path
	if len(remappedPath) > 255 {
		t.Errorf("expected remapped path length <= 255, got %d: %q", len(remappedPath), remappedPath)
	}
	if !strings.HasSuffix(remappedPath, ".mp3") {
		t.Errorf("expected path to preserve .mp3 extension, got %q", remappedPath)
	}
	if filepath.Ext(remappedPath) != ".mp3" {
		t.Errorf("expected extension .mp3, got %q", filepath.Ext(remappedPath))
	}
}
