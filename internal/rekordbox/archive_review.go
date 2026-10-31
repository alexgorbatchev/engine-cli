package rekordbox

import (
	"context"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type nativeOrigin struct {
	uuid string
	id   int64
}

type nativeTrack struct {
	id     int64
	path   string
	origin nativeOrigin
}

type archiveReview struct {
	tracks   map[string]nativeTrack
	stale    []string
	complete bool
}

// The caller's collection read barrier owns the Engine/companion observation.
// Archive presence alone says nothing about a partially synchronized document.
func reviewArchive(ctx context.Context, d db.Querier, src source, state importsource.State) (archiveReview, error) {
	tracks, err := readNativeTracks(ctx, d)
	if err != nil {
		return archiveReview{}, err
	}
	return reviewArchiveTracks(src, state, tracks), nil
}

func reviewArchiveTracks(src source, state importsource.State, tracks []nativeTrack) archiveReview {
	byOrigin := make(map[nativeOrigin]nativeTrack, len(tracks))
	for _, track := range tracks {
		byOrigin[track.origin] = track
	}
	review := archiveReview{tracks: map[string]nativeTrack{}, stale: []string{}}
	selectors := make(map[string]bool, len(src.selectors))
	for _, selector := range src.selectors {
		selectors[selector] = true
	}
	for _, mapping := range state.Tracks {
		if mapping.DocumentID != src.document.ID {
			continue
		}
		track, ok := byOrigin[nativeOrigin{uuid: mapping.OriginUUID, id: mapping.OriginTrackID}]
		if !ok || !selectors[mapping.SourceID] {
			review.stale = append(review.stale, mapping.SourceID)
			continue
		}
		review.tracks[mapping.SourceID] = track
	}
	review.complete = archiveComplete(src, state, review)
	return review
}

func archiveComplete(src source, state importsource.State, review archiveReview) bool {
	if len(review.stale) > 0 {
		return false
	}
	for _, document := range state.Documents {
		if document.ID != src.document.ID {
			continue
		}
		for _, selector := range src.selectors {
			if _, ok := review.tracks[selector]; !ok {
				return false
			}
		}
		return true
	}
	return false
}

func readNativeTracks(ctx context.Context, d db.Querier) ([]nativeTrack, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,COALESCE(path,''),COALESCE(originDatabaseUuid,''),COALESCE(originTrackId,0) FROM Track ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("reading import destination tracks: %w", err)
	}
	defer rows.Close()
	tracks := []nativeTrack{}
	for rows.Next() {
		var track nativeTrack
		if err := rows.Scan(&track.id, &track.path, &track.origin.uuid, &track.origin.id); err != nil {
			return nil, fmt.Errorf("reading import destination identity: %w", err)
		}
		tracks = append(tracks, track)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading import destination rows: %w", err)
	}
	return tracks, nil
}
