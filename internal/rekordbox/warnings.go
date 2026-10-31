package rekordbox

import "github.com/nateranda/djtools/rbxml"

const (
	sourceOnlyCode    = "import_source_only"
	verifiedMeter     = "4/4"
	cueMarkType       = 0
	loopMarkType      = 4
	nativeMarkerSlots = 8
)

// Warning identifies data retained in the source archive without claiming a
// native Engine representation. Values remain in the exact source document.
type Warning struct {
	Code     string `json:"code"`
	SourceID string `json:"source_id"`
	Field    string `json:"field"`
}

func sourceWarnings(s source) []Warning {
	warnings := []Warning{}
	if s.unmappedXML {
		warnings = append(warnings, Warning{Code: sourceOnlyCode, Field: "XML"})
	}
	for i, track := range s.xml.Collection.Tracks {
		for _, field := range sourceOnlyFields(track) {
			warnings = append(warnings, Warning{Code: sourceOnlyCode, SourceID: s.selectors[i], Field: field})
		}
	}
	return warnings
}

func sourceOnlyFields(t rbxml.Track) []string {
	fields := []string{}
	for _, field := range []struct {
		name    string
		present bool
	}{
		{"Grouping", t.Grouping != ""},
		{"Mix", t.Mix != ""},
		{"DiscNumber", t.DiscNumber != 0},
		{"TrackNumber", t.TrackNumber != 0},
		{"Colour", t.Colour != ""},
		{"PlayCount", t.PlayCount != 0},
		{"DateModified", t.DateModified != ""},
		{"LastPlayed", t.LastPlayed != ""},
		{"TempoMeter", unsupportedMeter(t)},
		{"PositionMarks", unsupportedMarks(t)},
	} {
		if field.present {
			fields = append(fields, field.name)
		}
	}
	return fields
}

func unsupportedMeter(t rbxml.Track) bool {
	if t.Tempo != nil {
		for _, tempo := range *t.Tempo {
			if tempo.Metro != verifiedMeter {
				return true
			}
		}
	}
	return false
}

func unsupportedMarks(t rbxml.Track) bool {
	if t.PositionMark != nil {
		for _, mark := range *t.PositionMark {
			if (mark.MarkType != cueMarkType && mark.MarkType != loopMarkType) || mark.Num < 0 || mark.Num >= nativeMarkerSlots {
				return true
			}
		}
	}
	return false
}
