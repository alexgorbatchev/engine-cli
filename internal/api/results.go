package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

// trackResult is one batch result whose track is reported with its playlist
// memberships once the whole batch has finished.
type trackResult interface {
	resultTrack() *db.Track
	value(track map[string]any) any
}

// trackResults reads playlist memberships for a whole batch in one query.
// Results completed before a failure are reported with that failure, so the
// lookup ignores cancellation of the batch.
func trackResults[T trackResult](ctx context.Context, d *sql.DB, items []T, runErr error) ([]any, error) {
	tracks := make([]*db.Track, len(items))
	for i, item := range items {
		tracks[i] = item.resultTrack()
	}
	values, err := details.Tracks(context.WithoutCancel(ctx), d, tracks...)
	if err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, err
	}
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item.value(values[i])
	}
	return result, runErr
}

type removedTrack struct{ track *db.Track }

func (r removedTrack) resultTrack() *db.Track       { return r.track }
func (removedTrack) value(track map[string]any) any { return track }

type addedTrack struct{ *track.AddTrackResult }

func (r addedTrack) resultTrack() *db.Track { return r.Track }
func (r addedTrack) value(track map[string]any) any {
	return map[string]any{"track": track, "playlist": r.Playlist, "moved": r.Moved}
}

type membershipChange struct {
	playlist *db.Playlist
	track    *db.Track
}

func (c membershipChange) resultTrack() *db.Track { return c.track }
func (c membershipChange) value(track map[string]any) any {
	return map[string]any{"playlist": c.playlist, "track": track}
}

type membershipMove struct {
	from, to *db.Playlist
	track    *db.Track
}

func (m membershipMove) resultTrack() *db.Track { return m.track }
func (m membershipMove) value(track map[string]any) any {
	return map[string]any{"from": m.from, "to": m.to, "track": track}
}

func upgradeResult(ctx context.Context, d *sql.DB, r *track.UpgradeResult) (map[string]any, error) {
	if r == nil {
		return nil, nil
	}
	t, err := details.OneTrack(ctx, d, r.Track)
	if err != nil {
		return nil, err
	}
	return map[string]any{"track": t, "original_path": r.OriginalPath, "timing_cleared": r.TimingCleared, "reanalysis_required": true}, nil
}
