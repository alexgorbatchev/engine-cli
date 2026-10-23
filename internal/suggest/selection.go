// Package suggest generates read-only metadata cleanup proposals.
package suggest

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
)

// Selection chooses tracks directly or through the direct memberships of playlists.
// Selectors are library-scoped positive IDs or opaque entity references.
type Selection struct {
	Tracks    []string `json:"tracks"`
	Playlists []string `json:"playlists"`
}

func (s Selection) Validate() error {
	if (len(s.Tracks) == 0) == (len(s.Playlists) == 0) {
		return fmt.Errorf("provide tracks or playlists")
	}
	selectors, kind := s.Tracks, "track"
	if len(s.Playlists) > 0 {
		selectors, kind = s.Playlists, "playlist"
	}
	if len(selectors) > details.MaxItems {
		return fmt.Errorf("provide at most %d selectors", details.MaxItems)
	}
	for _, selector := range selectors {
		if strings.HasPrefix(selector, "eng:") {
			parts := strings.Split(selector, ":")
			if len(parts) == 4 && parts[1] != "" && parts[2] == kind && parts[3] != "" {
				continue
			}
		} else if id, err := strconv.ParseInt(selector, 10, 64); err == nil && id > 0 {
			continue
		}
		return fmt.Errorf("use positive %s IDs or %s references", kind, kind)
	}
	return nil
}

// selectTracks resolves the entire selection before any LLM call. Missing
// selectors fail the request, and overlapping selections keep first-seen order.
func selectTracks(ctx context.Context, d *sql.DB, s Selection) (*details.Report, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ids := []int64{}
	seen := map[int64]bool{}
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, selector := range s.Tracks {
		t, err := db.FindTrack(ctx, d, selector)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("track %s is not in this library", selector)
		}
		add(t.ID)
	}
	for _, selector := range s.Playlists {
		p, err := db.FindPlaylist(ctx, d, selector)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, fmt.Errorf("playlist %s is not in this library", selector)
		}
		tracks, err := db.GetPlaylistTracks(ctx, d, p.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tracks {
			add(t.ID)
		}
	}
	result := &details.Report{Items: []details.Item{}}
	for start := 0; start < len(ids); start += details.MaxItems {
		batch, err := details.Read(ctx, d, "track", ids[start:min(start+details.MaxItems, len(ids))])
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, batch.Items...)
	}
	return result, nil
}
