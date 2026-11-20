package colors

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// MaxBatchTracks is the most tracks one batch color edit accepts.
const MaxBatchTracks = 500

// BatchOptions sets or clears one color on several tracks. A dry run reviews each
// track's before and after colors; applying requires that review's plan token.
type BatchOptions struct {
	Tracks    []string `json:"tracks"`
	Color     string   `json:"color" allowempty:"true"`
	Revision  string   `json:"revision"`
	DryRun    bool     `json:"dry_run,omitempty"`
	PlanToken string   `json:"plan_token,omitempty"`
}

// BatchTrack is one selected track's color before and after the change.
type BatchTrack struct {
	TrackID int64  `json:"track_id"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

// BatchChange is the reviewed, or applied, color change for the selected tracks.
type BatchChange struct {
	Revision      string       `json:"revision"`
	TrackIDs      []int64      `json:"track_ids"`
	Color         string       `json:"color"`
	PlanToken     string       `json:"plan_token"`
	ChangedTracks int          `json:"changed_tracks"`
	ClearedTracks int          `json:"cleared_tracks"`
	Tracks        []BatchTrack `json:"tracks"`
	Applied       bool         `json:"applied"`
}

func resolveBatch(ctx context.Context, engine *sql.DB, selectors []string) ([]companion.Track, error) {
	if len(selectors) == 0 || len(selectors) > MaxBatchTracks {
		return nil, failure("color_tracks_invalid", "select between one and 500 tracks")
	}
	result := []companion.Track{}
	seen := map[int64]bool{}
	for _, selector := range selectors {
		if strings.TrimSpace(selector) == "" {
			return nil, failure("color_tracks_invalid", "track selectors cannot be empty")
		}
		track, err := resolveTrack(ctx, engine, selector)
		if err != nil {
			return nil, err
		}
		if !seen[track.ID] {
			seen[track.ID] = true
			result = append(result, track)
		}
	}
	return result, nil
}

// batchPlan states every selected track's change and binds the selection, its native
// identities, the stored revision and the color into the plan token.
func batchPlan(state State, tracks []companion.Track, options BatchOptions) (*BatchChange, State, error) {
	color, err := normalize(options.Color)
	if err != nil {
		return nil, State{}, err
	}
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, State{}, err
	}
	result := &BatchChange{Revision: state.Revision(), TrackIDs: []int64{}, Color: color, Tracks: []BatchTrack{}}
	next := State{Assignments: slices.Clone(state.Assignments)}
	changed := map[companion.Origin]bool{}
	for _, track := range tracks {
		before := trackColor(state, track).Color
		result.TrackIDs = append(result.TrackIDs, track.ID)
		result.Tracks = append(result.Tracks, BatchTrack{TrackID: track.ID, Before: before, After: color})
		if before == color {
			continue
		}
		result.ChangedTracks++
		if color == "" {
			result.ClearedTracks++
		}
		if changed[track.Origin] {
			continue
		}
		changed[track.Origin] = true
		next.Assignments = slices.DeleteFunc(next.Assignments, func(a Assignment) bool {
			return a.OriginUUID == track.Origin.UUID && a.OriginTrackID == track.Origin.ID
		})
		if color != "" {
			next.Assignments = append(next.Assignments, Assignment{OriginUUID: track.Origin.UUID, OriginTrackID: track.Origin.ID, Color: color})
		}
	}
	data, err := json.Marshal(struct {
		Revision string
		Tracks   []companion.Track
		Color    string
	}{result.Revision, tracks, color})
	if err != nil {
		return nil, State{}, fmt.Errorf("encode track-color batch review: %w", err)
	}
	hash := sha256.Sum256(data)
	result.PlanToken = hex.EncodeToString(hash[:])
	return result, next, nil
}

func previewBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) (*BatchChange, State, []companion.Track, error) {
	tracks, err := resolveBatch(ctx, engine, options.Tracks)
	if err != nil {
		return nil, State{}, nil, err
	}
	state, err := Read(ctx, library)
	if err != nil {
		return nil, State{}, nil, err
	}
	result, next, err := batchPlan(state, tracks, options)
	if err != nil {
		return nil, State{}, nil, err
	}
	if !options.DryRun && (options.PlanToken == "" || result.PlanToken != options.PlanToken) {
		return nil, State{}, nil, failure("color_plan_stale", "review the selected tracks and their colors before applying the change")
	}
	if !options.DryRun && result.ChangedTracks == 0 {
		return nil, State{}, nil, failure("color_no_changes", "the selected tracks already have this color")
	}
	return result, next, tracks, nil
}

// CheckBatch runs before safety enrollment; AssignBatch repeats the review under the
// collection write barrier and the store's revision check.
func CheckBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) error {
	_, _, _, err := previewBatch(ctx, engine, library, options)
	return err
}

// AssignBatch reviews the change on a dry run, or applies exactly the reviewed plan in
// one transaction.
func AssignBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) (*BatchChange, error) {
	result, next, tracks, err := previewBatch(ctx, engine, library, options)
	if err != nil || options.DryRun {
		return result, err
	}
	saved, err := replaceChecked(ctx, library, options.Revision, next, func(current State) error {
		checked, _, err := batchPlan(current, tracks, options)
		if err != nil {
			return err
		}
		if checked.PlanToken != options.PlanToken {
			return failure("color_plan_stale", "the selected tracks changed; review the colors again")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result.Revision = saved.Revision()
	result.Applied = true
	return result, nil
}
