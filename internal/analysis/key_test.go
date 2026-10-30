package analysis

import (
	"context"
	"testing"

	"github.com/cabbagekobe/mixedkey"
	"github.com/nateranda/djtools/musicalkey"
)

func TestEveryDetectedKeyMapsToTheEngineIndexOfItsCamelotName(t *testing.T) {
	for tonic := range 12 {
		for _, mode := range []mixedkey.Mode{mixedkey.Major, mixedkey.Minor} {
			key := mixedkey.Key{Tonic: mixedkey.PitchClass(tonic), Mode: mode}
			want, err := musicalkey.Parse(key.Camelot())
			if err != nil {
				t.Fatal(err)
			}
			if got := musicalkey.FromPitchClass(tonic, mode == mixedkey.Minor); got != want {
				t.Fatalf("%s: index %d, want %d", key.Camelot(), got, want)
			}
		}
	}
}

func TestMajorKeyProgressionIsDetectedAsItsMajorKey(t *testing.T) {
	cMajor := [][]float64{
		{261.63, 329.63, 392},         // C major
		{349.23, 440, 523.25},         // F major
		{392, 493.88, 587.33},         // G major
		{261.63, 329.63, 392, 523.25}, // C major
	}
	samples, _ := synthesize(0, cMajor, section{bpm: 124, bars: 40})
	result, err := Samples(context.Background(), samples, Options{Key: true})
	if err != nil || result.Key == nil {
		t.Fatal(err, result.KeyErr)
	}
	if result.Key.Camelot != "8B" || result.Key.Index != 0 {
		t.Fatalf("key=%+v", result.Key)
	}
}
