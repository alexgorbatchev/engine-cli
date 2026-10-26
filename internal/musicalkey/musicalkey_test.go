package musicalkey

import (
	"strings"
	"testing"
)

// Engine DJ 5.1.0's own name tables in stored-index order, from engine-dj-app's src/tonality.rs.
var engineTables = map[string]string{
	"sharps":   "C Am G Em D Bm A F#m E C#m B G#m F# D#m C# A#m G# Fm D# Cm A# Gm F Dm",
	"flats":    "C Am G Em D Bm A Gbm E Dbm B Abm Gb Ebm Db Bbm Ab Fm Eb Cm Bb Gm F Dm",
	"open key": "1d 1m 2d 2m 3d 3m 4d 4m 5d 5m 6d 6m 7d 7m 8d 8m 9d 9m 10d 10m 11d 11m 12d 12m",
	"camelot":  "8B 8A 9B 9A 10B 10A 11B 11A 12B 12A 1B 1A 2B 2A 3B 3A 4B 4A 5B 5A 6B 6A 7B 7A",
}

func TestParseEngineTables(t *testing.T) {
	for table, names := range engineTables {
		for want, name := range strings.Fields(names) {
			got, err := Parse(name)
			if err != nil || got != int64(want) {
				t.Fatalf("%s %q = %d, %v; want %d", table, name, got, err, want)
			}
		}
	}
}

func TestParseSpellings(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int64
	}{
		{" 8a ", 1},
		{"6b", 20},
		{"12M", 23},
		{"A minor", 1},
		{"amin", 1},
		{"a", 6},
		{"BM", 10},
		{"bm", 5},
		{"Bbm", 15},
		{"B♭ minor", 15},
		{"F♯ Major", 12},
		{"Cb", 10},
		{"B#m", 19},
		{"NONE", None},
	} {
		got, err := Parse(tt.text)
		if err != nil || got != tt.want {
			t.Fatalf("Parse(%q) = %d, %v; want %d", tt.text, got, err, tt.want)
		}
	}
}

func TestParseRejectsOtherText(t *testing.T) {
	for _, text := range []string{"", "0A", "13A", "8C", "H", "Am7", "C##", "-1", "20"} {
		if got, err := Parse(text); err == nil {
			t.Fatalf("Parse(%q) = %d, want an error", text, got)
		}
	}
}
