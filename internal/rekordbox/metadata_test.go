package rekordbox

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func TestNativeMetadataUsesAuthoritativeAudioAndMaintainedKeyRating(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Name="Title" Artist="Artist" Album="Album" Genre="Genre" Comments="Notes" Label="Label" Composer="Composer" Remixer="Remixer" Year="2026" AverageBpm="128.125" BitRate="320" TotalTime="99999" SampleRate="1" Size="1" Kind="Wrong source extension" Tonality="Am" Rating="255" Location="file:///music/import.WAV"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	track, err := nativeMetadata(src.xml.Collection.Tracks[0], src.library.Songs[0], audioExtent{rate: 48000, frames: 480001, bytes: 960002})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root := t.TempDir()
	if _, err := db.CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, err := db.InsertTrack(ctx, d, &track)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTrackByID(ctx, d, id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Length.Int64 != 10 || got.FileBytes.Int64 != 960002 || got.FileType.String != "wav" || got.BPMAnalyzed.Float64 != 128.125 || got.BPM.Int64 != 128 || got.Rating.Int64 != 100 || got.Key.Int64 != int64(src.library.Songs[0].Key) || got.IsAnalyzed.Bool || !got.IsAvailable.Bool {
		t.Fatalf("source estimates replaced native observation or metadata changed: %+v", got)
	}
	if got.Title.String != "Title" || got.Artist.String != "Artist" || got.Album.String != "Album" || got.Genre.String != "Genre" || got.Comment.String != "Notes" || got.Label.String != "Label" || got.Composer.String != "Composer" || got.Remixer.String != "Remixer" || got.Year.Int64 != 2026 {
		t.Fatalf("supported metadata lost: %+v", got)
	}
}

func TestNativeMetadataRejectsUnsafeNumericNarrowing(t *testing.T) {
	valid := audioExtent{rate: 48000, frames: 480000, bytes: 100}
	for name, audio := range map[string]audioExtent{
		"empty": {}, "negative frames": {rate: 48000, frames: -1},
		"inexact frames": {rate: 48000, frames: 1<<53 + 1},
		"invalid bytes":  {rate: 48000, frames: 480000, bytes: -1},
		"NaN rate":       {rate: math.NaN(), frames: 480000}, "infinite rate": {rate: math.Inf(1), frames: 480000},
		"duration overflow": {rate: math.SmallestNonzeroFloat64, frames: 480000},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := nativeMetadata(rbxml.Track{}, lib.Song{}, audio); err == nil || got.Title.Valid {
				t.Fatalf("invalid audio produced metadata: %+v, %v", got, err)
			}
		})
	}
	for _, raw := range []rbxml.Track{{AverageBpm: math.NaN()}, {AverageBpm: math.Inf(1)}, {AverageBpm: -1}, {AverageBpm: 1e100}, {Year: -1}, {BitRate: -1}} {
		if got, err := nativeMetadata(raw, lib.Song{}, valid); err == nil || got.Title.Valid {
			t.Fatalf("invalid metadata narrowed into native integer: %+v, %v", got, err)
		}
	}
}
