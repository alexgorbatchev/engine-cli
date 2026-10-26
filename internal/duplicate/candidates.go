package duplicate

import (
	"context"
	"database/sql"
)

// Candidates compares an external track with the collection using the same
// matching rules as duplicate listing. Metadata matches are candidate evidence.
func Candidates(ctx context.Context, d *sql.DB, target Track, mode string) ([]Track, error) {
	if err := validateMode(mode); err != nil {
		return nil, err
	}
	tracks, err := readTracks(ctx, d)
	if err != nil {
		return nil, err
	}
	if err := attachReferences(ctx, d, tracks); err != nil {
		return nil, err
	}
	target.ID = 0
	groups := groupTracks(append([]Track{target}, tracks...), mode)
	result := make([]Track, 0)
	for _, group := range groups {
		if group.Tracks[0].ID != 0 {
			continue
		}
		memberships, err := readPlaylistMemberships(ctx, d, group.Tracks[1:])
		if err != nil {
			return nil, err
		}
		for _, candidate := range group.Tracks[1:] {
			if names, ok := memberships[candidate.ID]; ok {
				candidate.Playlists = names
			}
			result = append(result, candidate)
		}
	}
	return result, nil
}
