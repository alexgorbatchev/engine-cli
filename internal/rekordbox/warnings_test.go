package rekordbox

import (
	"slices"
	"testing"

	"github.com/nateranda/djtools/rbxml"
)

func TestSourceWarningsRetainIdentityAndUnsupportedScope(t *testing.T) {
	tests := []struct {
		name   string
		track  rbxml.Track
		fields []string
	}{
		{"empty", rbxml.Track{}, []string{}},
		{"ordinary metadata", rbxml.Track{Name: "Title", Artist: "Artist", Album: "Album", Genre: "Genre", Comments: "Notes"}, []string{}},
		{"played date does not invent native history", rbxml.Track{LastPlayed: "2026-10-30"}, []string{"LastPlayed"}},
		{"source-only metadata", rbxml.Track{Grouping: "Group", Mix: "Mix", DiscNumber: 2, TrackNumber: 3, Colour: "0xFF0000", PlayCount: 4, DateModified: "2026-10-30"}, []string{"Grouping", "Mix", "DiscNumber", "TrackNumber", "Colour", "PlayCount", "DateModified"}},
		{"verified meter", rbxml.Track{Tempo: &[]rbxml.Tempo{{Metro: "4/4"}}}, []string{}},
		{"unsupported meter", rbxml.Track{Tempo: &[]rbxml.Tempo{{Metro: "4/4"}, {Metro: "3/4"}}}, []string{"TempoMeter"}},
		{"missing meter", rbxml.Track{Tempo: &[]rbxml.Tempo{{Bpm: 120}}}, []string{"TempoMeter"}},
		{"native edge slots", rbxml.Track{PositionMark: &[]rbxml.PositionMark{{MarkType: 0, Num: 0}, {MarkType: 4, Num: 7}}}, []string{}},
		{"memory mark", rbxml.Track{PositionMark: &[]rbxml.PositionMark{{MarkType: 0, Num: -1}}}, []string{"PositionMarks"}},
		{"extra slot", rbxml.Track{PositionMark: &[]rbxml.PositionMark{{MarkType: 4, Num: 8}}}, []string{"PositionMarks"}},
		{"load mark", rbxml.Track{PositionMark: &[]rbxml.PositionMark{{MarkType: 3, Num: 0}}}, []string{"PositionMarks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := source{xml: rbxml.Document{Collection: rbxml.Collection{Tracks: []rbxml.Track{{}, tt.track}}}, selectors: []string{"id:0", "ordinal:2"}}
			got := sourceWarnings(s)
			fields := []string{}
			for _, warning := range got {
				if warning.SourceID != "ordinal:2" || warning.Code != "import_source_only" {
					t.Fatalf("warning lost source identity or safe code: %+v", warning)
				}
				fields = append(fields, warning.Field)
			}
			if !slices.Equal(fields, tt.fields) {
				t.Fatalf("source-only scope: got %v, want %v", fields, tt.fields)
			}
		})
	}
}
