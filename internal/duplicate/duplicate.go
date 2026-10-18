// Package duplicate finds candidate duplicates and consolidates explicitly selected tracks.
package duplicate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Track contains comparison information. Matching metadata is not proof of identical audio.
type Track struct {
	ID             int64    `json:"id"`
	Title          string   `json:"title"`
	Artist         string   `json:"artist"`
	Album          string   `json:"album"`
	Filename       string   `json:"filename"`
	Path           string   `json:"path"`
	Length         int64    `json:"length"`
	Bitrate        int64    `json:"bitrate"`
	FileBytes      int64    `json:"file_bytes"`
	FileType       string   `json:"file_type"`
	TimeLastPlayed int64    `json:"time_last_played"`
	IsPlayed       bool     `json:"is_played"`
	HasCues        bool     `json:"has_cues"`
	HasLoops       bool     `json:"has_loops"`
	Playlists      []string `json:"playlists,omitempty"`
	originID       int64
	originUUID     string
}

type Group struct {
	Tracks []Track `json:"tracks"`
}

// Both database connections and transactions support consistent comparison reads.
type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readTracks(ctx context.Context, d reader) ([]Track, error) {
	rows, err := d.QueryContext(ctx, `SELECT t.id,COALESCE(t.title,''),COALESCE(t.artist,''),COALESCE(t.album,''),
	COALESCE(t.filename,''),COALESCE(t.path,''),COALESCE(t.length,0),COALESCE(t.bitrate,0),
	COALESCE(t.fileBytes,0),COALESCE(t.fileType,''),COALESCE(t.timeLastPlayed,0),COALESCE(t.isPlayed,0),
	COALESCE(length(p.quickCues)>0,0),COALESCE(length(p.loops)>0,0),
	COALESCE(t.originTrackId,0),COALESCE(t.originDatabaseUuid,'')
	FROM Track t LEFT JOIN PerformanceData p ON p.trackId=t.id ORDER BY t.id`)
	if err != nil {
		return nil, fmt.Errorf("reading duplicate candidates: %w", err)
	}
	defer rows.Close()
	var tracks []Track
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Filename, &t.Path, &t.Length, &t.Bitrate, &t.FileBytes, &t.FileType, &t.TimeLastPlayed, &t.IsPlayed, &t.HasCues, &t.HasLoops, &t.originID, &t.originUUID); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, rows.Err()
}

func validateMode(mode string) error {
	switch mode {
	case "all", "metadata", "filename-len":
		return nil
	}
	return fmt.Errorf("unknown duplicate type %q; use all, metadata, or filename-len", mode)
}

// List returns deterministic, non-overlapping connected groups of candidates.
func List(ctx context.Context, d *sql.DB, mode string) ([]Group, error) {
	if err := validateMode(mode); err != nil {
		return nil, err
	}
	tracks, err := readTracks(ctx, d)
	if err != nil {
		return nil, err
	}
	return groupTracks(tracks, mode), nil
}

func normalized(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func groupTracks(tracks []Track, mode string) []Group {
	// A track can connect a metadata group and a filename group. Union their
	// roots so it is reported once, even when several matching rules overlap.
	parents := make([]int, len(tracks))
	for i := range parents {
		parents[i] = i
	}
	root := func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	type key struct {
		kind, first, second string
		length              int64
	}
	seen := make(map[key]int)
	for i, t := range tracks {
		var keys []key
		if a, b := normalized(t.Artist), normalized(t.Title); mode != "filename-len" && a != "" && b != "" {
			keys = append(keys, key{kind: "metadata", first: a, second: b})
		}
		if f := normalized(t.Filename); mode != "metadata" && f != "" && t.Length > 0 {
			keys = append(keys, key{kind: "filename", first: f, length: t.Length})
		}
		for _, k := range keys {
			if prev, ok := seen[k]; ok {
				parents[root(i)] = root(prev)
			} else {
				seen[k] = i
			}
		}
	}
	byRoot := make(map[int][]Track)
	for i, t := range tracks {
		r := root(i)
		byRoot[r] = append(byRoot[r], t)
	}
	groups := make([]Group, 0)
	for _, ts := range byRoot {
		if len(ts) > 1 {
			groups = append(groups, Group{Tracks: ts})
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Tracks[0].ID < groups[j].Tracks[0].ID })
	return groups
}

func lookup(tracks []Track, id int64) (Track, error) {
	for _, t := range tracks {
		if t.ID == id {
			return t, nil
		}
	}
	return Track{}, fmt.Errorf("track ID %d not found in collection", id)
}

// Inspect includes playlists resolved through the track's persistent origin identity.
func Inspect(ctx context.Context, d *sql.DB, id int64) (*Track, error) {
	tracks, err := readTracks(ctx, d)
	if err != nil {
		return nil, err
	}
	t, err := lookup(tracks, id)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `SELECT COALESCE(p.title,'') FROM PlaylistEntity pe JOIN Playlist p ON p.id=pe.listId
	WHERE pe.trackId=? AND pe.databaseUuid=? ORDER BY p.id`, t.originID, t.originUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		t.Playlists = append(t.Playlists, name)
	}
	return &t, rows.Err()
}
