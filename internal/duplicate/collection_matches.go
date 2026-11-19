package duplicate

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// SameLengthTolerance is the largest difference in seconds between an
// advertised or decoded length and a collection track's length that still
// counts as the same length when acquisition metadata is matched against the
// collection. Engine stores track lengths in whole seconds, so a tighter bound
// would split the same recording on rounding alone.
const SameLengthTolerance = 2.0

// CollectionMatch is a collection track whose artist and title match
// acquisition metadata. A match compares metadata and length, never the audio
// itself: SameLength is the strongest evidence available before a download.
type CollectionMatch struct {
	TrackID    int64   `json:"track_id"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Album      string  `json:"album,omitempty"`
	Format     string  `json:"format,omitempty"`
	Bitrate    int64   `json:"bitrate,omitempty"`
	Duration   float64 `json:"duration"`
	SameLength bool    `json:"same_length"`
}

// CollectionIndex matches acquisition metadata against one read of the
// collection's tracks.
type CollectionIndex struct {
	tracks []Track
}

// ReadCollectionIndex reads the collection's tracks once for repeated matching.
func ReadCollectionIndex(ctx context.Context, d reader) (*CollectionIndex, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,COALESCE(title,''),COALESCE(artist,''),COALESCE(album,''),
	COALESCE(length,0),COALESCE(bitrate,0),COALESCE(fileType,'') FROM Track ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("reading collection tracks: %w", err)
	}
	defer rows.Close()
	var tracks []Track
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Length, &t.Bitrate, &t.FileType); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &CollectionIndex{tracks: tracks}, nil
}

// Match returns the collection tracks whose artist and title match, in track ID
// order. Titles are compared with trailing versions in parentheses or brackets
// set aside, so "Undertow Signal (Radio Edit)" matches "Undertow Signal
// (Original Mix)": another edit of the same title is reported, and SameLength
// tells the edits apart. A duration of zero or less is unknown, so no match
// has the same length.
func (c *CollectionIndex) Match(artist, title string, duration float64) []CollectionMatch {
	artistKey, titleKey := matchKey(artist), baseTitle(title)
	if artistKey == "" || titleKey == "" {
		return []CollectionMatch{}
	}
	matches := make([]CollectionMatch, 0)
	for _, track := range c.tracks {
		if matchKey(track.Artist) != artistKey || baseTitle(track.Title) != titleKey {
			continue
		}
		matches = append(matches, collectionMatch(track, duration))
	}
	return matches
}

// MatchQuery returns the collection tracks a free-text search names: "Artist -
// Title" matches by artist and title, and any query also matches tracks whose
// title, or artist and title together, equal it.
func (c *CollectionIndex) MatchQuery(query string) []CollectionMatch {
	artist, title := SplitArtistTitle(query)
	queryKey := matchKey(query)
	matches := make([]CollectionMatch, 0)
	if queryKey == "" {
		return matches
	}
	for _, track := range c.tracks {
		trackTitle := baseTitle(track.Title)
		byParts := artist != "" && matchKey(track.Artist) == matchKey(artist) && trackTitle == baseTitle(title)
		whole := trackTitle != "" && (trackTitle == baseTitle(query) || matchKey(track.Artist+" "+track.Title) == queryKey || matchKey(track.Artist)+" "+trackTitle == baseTitle(query))
		if byParts || whole {
			matches = append(matches, collectionMatch(track, 0))
		}
	}
	return matches
}

// SplitArtistTitle splits "Artist - Title" at its first spaced dash. Text
// without one has no artist.
func SplitArtistTitle(text string) (artist, title string) {
	for _, separator := range []string{" - ", " – ", " — "} {
		if before, after, ok := strings.Cut(text, separator); ok && strings.TrimSpace(before) != "" && strings.TrimSpace(after) != "" {
			return strings.TrimSpace(before), strings.TrimSpace(after)
		}
	}
	return "", strings.TrimSpace(text)
}

func collectionMatch(track Track, duration float64) CollectionMatch {
	return CollectionMatch{
		TrackID:    track.ID,
		Title:      track.Title,
		Artist:     track.Artist,
		Album:      track.Album,
		Format:     track.FileType,
		Bitrate:    track.Bitrate,
		Duration:   float64(track.Length),
		SameLength: duration > 0 && track.Length > 0 && math.Abs(duration-float64(track.Length)) <= SameLengthTolerance,
	}
}

// matchKey folds case and runs of whitespace.
func matchKey(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// baseTitle is a title's match key with trailing versions in parentheses or
// brackets set aside, such as "(Radio Edit)" or "[Official Video]".
func baseTitle(title string) string {
	key := matchKey(title)
	for {
		trimmed := strings.TrimSpace(key)
		closing := map[byte]byte{')': '(', ']': '['}
		if trimmed == "" {
			return ""
		}
		opening, ok := closing[trimmed[len(trimmed)-1]]
		if !ok {
			return trimmed
		}
		start := strings.LastIndexByte(trimmed, opening)
		if start <= 0 {
			return trimmed
		}
		key = trimmed[:start]
	}
}
