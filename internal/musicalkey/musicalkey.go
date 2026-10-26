// Package musicalkey maps musical key names to the index Engine stores in Track.key.
//
// Engine's index walks the circle of fifths from C major: even indexes are major keys and each odd index is the
// preceding major's relative minor, so 0 is C major (8B, 1d), 1 is A minor (8A, 1m), and 23 is D minor (7A, 12m).
// The tables match Engine DJ 5.1.0's own key names, recorded in engine-dj-app's tonality translation.
package musicalkey

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Max is the highest stored key index; None asks for the key to be cleared.
const (
	Max  = 23
	None = -1
)

var (
	camelotPattern = regexp.MustCompile(`^(1[0-2]|[1-9])([ABab])$`)
	openKeyPattern = regexp.MustCompile(`^(1[0-2]|[1-9])([dDmM])$`)
	// A note, an optional sharp or flat, and an optional mode. Like Engine, a bare "M" means major and "m" minor.
	namePattern  = regexp.MustCompile(`^([A-Ga-g])([#♯b♭]?)\s*(M|m|(?i:maj|major|min|minor))?$`)
	pitchClasses = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}
)

// Parse reads a key written in Camelot (8A), Open Key (1m), or as a name (Am, F#m, Bb, A minor), returning its stored
// index. "none" returns None.
func Parse(text string) (int64, error) {
	value := strings.TrimSpace(text)
	if strings.EqualFold(value, "none") {
		return None, nil
	}
	if m := camelotPattern.FindStringSubmatch(value); m != nil {
		// The circle of fifths starts at Camelot 8: C major and A minor.
		return index((number(m[1])+4)%12, strings.EqualFold(m[2], "a")), nil
	}
	if m := openKeyPattern.FindStringSubmatch(value); m != nil {
		return index(number(m[1])-1, strings.EqualFold(m[2], "m")), nil
	}
	if m := namePattern.FindStringSubmatch(value); m != nil {
		pitch := pitchClasses[strings.ToLower(m[1])[0]]
		switch m[2] {
		case "#", "♯":
			pitch++
		case "b", "♭":
			pitch--
		}
		isMinor := m[3] == "m" || strings.HasPrefix(strings.ToLower(m[3]), "min")
		if isMinor {
			// A minor key shares its position on the circle with its relative major, three semitones higher.
			pitch += 3
		}
		// Each fifth is seven semitones, and seven is its own inverse modulo twelve.
		return index((pitch+12)%12*7%12, isMinor), nil
	}
	return 0, fmt.Errorf("%q is not a musical key; use Camelot like 8A, Open Key like 1m, a name like Am or F#, or none", text)
}

// index converts a position on the circle of fifths, counted from C major, into Engine's stored index.
func index(fifths int, isMinor bool) int64 {
	result := int64(fifths * 2)
	if isMinor {
		result++
	}
	return result
}

// number reads a wheel number the key patterns have already limited to 1–12.
func number(digits string) int {
	n, err := strconv.Atoi(digits)
	if err != nil {
		panic(fmt.Sprintf("musicalkey: pattern admitted non-numeric wheel number %q", digits))
	}
	return n
}
