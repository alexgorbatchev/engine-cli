package rekordbox

import (
	"errors"
	"fmt"

	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

// Rekordbox's supported XML position-mark schema has no RGB fields. Preview
// discloses this assigned display color; it is not an imported source color.
const importedMarkerColor = "#FFFFFF"

type performance struct {
	beat, cues, loops []byte
}

func preparePerformance(raw rbxml.Track, song lib.Song, rate float64, frames int64) (performance, error) {
	if frames <= 0 || frames > 1<<53 {
		return performance{}, errors.New("audio has no exact representable sample extent")
	}
	grid := song.Grid
	if unsupportedMeter(raw) {
		grid = nil // Preserve unsupported meters in the source archive, never invent 4/4.
	}
	beat, err := engine.EncodeTempoGrid(rate, float64(frames), grid)
	if err != nil {
		return performance{}, fmt.Errorf("preparing tempo grid: %w", err)
	}
	markers := importedMarkers(song, rate, frames)
	saved, err := engine.EncodeSavedMarkers(markers)
	if err != nil {
		return performance{}, fmt.Errorf("preparing saved positions: %w", err)
	}
	return performance{beat: beat, cues: saved.QuickCues, loops: saved.Loops}, nil
}

func importedMarkers(song lib.Song, rate float64, frames int64) engine.SavedMarkers {
	markers := engine.SavedMarkers{SampleRate: rate, SampleLimit: float64(frames)}
	// The maintained parser maps source Num to native Position by adding one.
	// Memory cues and extra slots stay archived and are reported by sourceWarnings.
	for _, cue := range song.Cues {
		if cue.Position >= 1 && cue.Position <= nativeMarkerSlots {
			cue.Color = importedMarkerColor
			markers.HotCues = append(markers.HotCues, cue)
		}
	}
	for _, loop := range song.Loops {
		if loop.Position >= 1 && loop.Position <= nativeMarkerSlots {
			loop.Color = importedMarkerColor
			markers.Loops = append(markers.Loops, loop)
		}
	}
	// Memory/load marks do not establish Engine's native main-cue semantics.
	return markers
}
