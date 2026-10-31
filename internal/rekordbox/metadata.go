package rekordbox

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

type audioExtent struct {
	rate   float64
	frames int64
	bytes  int64
}

func nativeMetadata(raw rbxml.Track, song lib.Song, audio audioExtent) (db.Track, error) {
	seconds := float64(audio.frames) / audio.rate
	if audio.frames <= 0 || audio.frames > 1<<53 || audio.bytes < 0 || !finiteNonnegative(audio.rate) || audio.rate == 0 || !finiteNonnegative(seconds) || seconds >= math.MaxInt64 {
		return db.Track{}, fmt.Errorf("audio has no representable native extent")
	}
	if !finiteNonnegative(raw.AverageBpm) || raw.AverageBpm >= math.MaxInt64 || raw.Year < 0 || raw.BitRate < 0 {
		return db.Track{}, fmt.Errorf("source metadata has invalid numeric values")
	}
	return db.Track{
		Title: text(raw.Name), Artist: text(raw.Artist), Album: text(raw.Album), Genre: text(raw.Genre),
		Comment: text(raw.Comments), Label: text(raw.Label), Composer: text(raw.Composer), Remixer: text(raw.Remixer),
		Path: text(song.Path), Filename: text(filepath.Base(song.Path)),
		FileType: text(strings.TrimPrefix(strings.ToLower(filepath.Ext(song.Path)), ".")),
		Length:   number(int64(seconds)), FileBytes: number(audio.bytes), Bitrate: number(int64(raw.BitRate)),
		BPM: number(int64(raw.AverageBpm)), BPMAnalyzed: sql.NullFloat64{Float64: raw.AverageBpm, Valid: true},
		Year: number(int64(raw.Year)), Key: number(int64(song.Key)), Rating: number(int64(song.Rating)),
		IsAvailable: sql.NullBool{Bool: true, Valid: true}, IsAnalyzed: sql.NullBool{Bool: false, Valid: true},
	}, nil
}

func text(value string) sql.NullString { return sql.NullString{String: value, Valid: true} }
func number(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}
