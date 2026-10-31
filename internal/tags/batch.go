package tags

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

const maxBatchTracks = 500

type BatchOptions struct {
	Tracks    []string `json:"tracks"`
	Revision  string   `json:"revision"`
	Add       []string `json:"add" allowempty:"true"`
	Remove    []string `json:"remove" allowempty:"true"`
	DryRun    bool     `json:"dry_run"`
	PlanToken string   `json:"plan_token,omitempty"`
}

type BatchValue struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SelectedTracks int    `json:"selected_tracks"`
}

type BatchCategory struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Values []BatchValue `json:"values"`
}

type BatchTags struct {
	Revision   string          `json:"revision"`
	TrackIDs   []int64         `json:"track_ids"`
	Categories []BatchCategory `json:"categories"`
}

type BatchChange struct {
	Revision           string   `json:"revision"`
	TrackIDs           []int64  `json:"track_ids"`
	Add                []string `json:"add"`
	Remove             []string `json:"remove"`
	PlanToken          string   `json:"plan_token"`
	ChangedTracks      int      `json:"changed_tracks"`
	AddedAssignments   int      `json:"added_assignments"`
	RemovedAssignments int      `json:"removed_assignments"`
	Applied            bool     `json:"applied"`
}

type batchTrack struct {
	ID     int64            `json:"id"`
	Origin companion.Origin `json:"origin"`
}

func resolveBatch(ctx context.Context, engine *sql.DB, selectors []string) ([]batchTrack, error) {
	if len(selectors) == 0 || len(selectors) > maxBatchTracks {
		return nil, failure("tag_tracks_invalid", "select between one and 500 tracks")
	}
	result := []batchTrack{}
	seen := map[int64]bool{}
	for _, selector := range selectors {
		if strings.TrimSpace(selector) == "" {
			return nil, failure("tag_tracks_invalid", "track selectors cannot be empty")
		}
		id, uuid, originID, err := trackOrigin(ctx, engine, selector)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, batchTrack{ID: id, Origin: companion.Origin{UUID: uuid, ID: originID}})
		}
	}
	return result, nil
}

func batchTrackIDs(tracks []batchTrack) []int64 {
	ids := make([]int64, len(tracks))
	for i, track := range tracks {
		ids[i] = track.ID
	}
	return ids
}

func InspectBatch(ctx context.Context, engine *sql.DB, library string, selectors []string) (*BatchTags, error) {
	tracks, err := resolveBatch(ctx, engine, selectors)
	if err != nil {
		return nil, err
	}
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	assigned := batchAssignments(s)
	result := &BatchTags{Revision: revision(s), TrackIDs: batchTrackIDs(tracks), Categories: []BatchCategory{}}
	for _, c := range s.Categories {
		category := BatchCategory{ID: c.ID, Name: c.Name, Values: []BatchValue{}}
		for _, v := range c.Values {
			value := BatchValue{ID: v.ID, Name: v.Name}
			for _, track := range tracks {
				if assigned[track.Origin][v.ID] {
					value.SelectedTracks++
				}
			}
			category.Values = append(category.Values, value)
		}
		result.Categories = append(result.Categories, category)
	}
	return result, nil
}

func batchAssignments(state State) map[companion.Origin]map[string]bool {
	assigned := map[companion.Origin]map[string]bool{}
	for _, a := range state.Assignments {
		origin := companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}
		if assigned[origin] == nil {
			assigned[origin] = map[string]bool{}
		}
		assigned[origin][a.ValueID] = true
	}
	return assigned
}

func batchPlan(state State, tracks []batchTrack, options BatchOptions) (*BatchChange, error) {
	if err := checkRevision(options.Revision, state); err != nil {
		return nil, err
	}
	if options.Add == nil || options.Remove == nil {
		return nil, failure("tag_values_required", "provide explicit add and remove lists; empty lists preserve assignments")
	}
	add, remove := slices.Clone(options.Add), slices.Clone(options.Remove)
	slices.Sort(add)
	slices.Sort(remove)
	if len(slices.Compact(slices.Clone(add))) != len(add) || len(slices.Compact(slices.Clone(remove))) != len(remove) {
		return nil, failure("tag_value_duplicate", "each custom-tag action may name a value only once")
	}
	known := map[string]bool{}
	for _, c := range state.Categories {
		for _, v := range c.Values {
			known[v.ID] = true
		}
	}
	for _, id := range append(slices.Clone(add), remove...) {
		if !known[id] {
			return nil, failure("tag_not_found", "a selected custom-tag value no longer exists")
		}
	}
	for _, id := range add {
		if slices.Contains(remove, id) {
			return nil, failure("tag_actions_overlap", "a value cannot be added and removed in the same change")
		}
	}
	result := &BatchChange{Revision: revision(state), TrackIDs: batchTrackIDs(tracks), Add: add, Remove: remove}
	assigned, counted := batchAssignments(state), map[companion.Origin]bool{}
	for _, track := range tracks {
		added, removed := 0, 0
		for _, id := range add {
			if !assigned[track.Origin][id] {
				added++
			}
		}
		for _, id := range remove {
			if assigned[track.Origin][id] {
				removed++
			}
		}
		if added+removed > 0 {
			result.ChangedTracks++
		}
		if !counted[track.Origin] {
			counted[track.Origin] = true
			result.AddedAssignments += added
			result.RemovedAssignments += removed
		}
	}
	// The exact resolved selection and native identities are part of consent.
	data, err := json.Marshal(struct {
		Revision    string
		Tracks      []batchTrack
		Add, Remove []string
	}{result.Revision, tracks, add, remove})
	if err != nil {
		return nil, fmt.Errorf("encode custom-tag batch review: %w", err)
	}
	hash := sha256.Sum256(data)
	result.PlanToken = hex.EncodeToString(hash[:])
	return result, nil
}

func CheckBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) error {
	_, err := previewBatch(ctx, engine, library, options)
	return err
}

func previewBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) (*BatchChange, error) {
	tracks, err := resolveBatch(ctx, engine, options.Tracks)
	if err != nil {
		return nil, err
	}
	state, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	result, err := batchPlan(state, tracks, options)
	if err != nil {
		return nil, err
	}
	if !options.DryRun && (options.PlanToken == "" || result.PlanToken != options.PlanToken) {
		return nil, failure("tag_plan_stale", "review the selected tracks and custom-tag changes before applying them")
	}
	if !options.DryRun && result.ChangedTracks == 0 {
		return nil, failure("tag_no_changes", "the selected tracks already have the reviewed custom-tag assignments")
	}
	return result, nil
}

func AssignBatch(ctx context.Context, engine *sql.DB, library string, options BatchOptions) (*BatchChange, error) {
	result, err := previewBatch(ctx, engine, library, options)
	if err != nil || options.DryRun {
		return result, err
	}
	mutation, err := mutate(ctx, library, options.Revision, func(tx *sql.Tx, state State) (string, bool, error) {
		tracks, err := resolveBatch(ctx, engine, options.Tracks)
		if err != nil {
			return "", false, err
		}
		checked, err := batchPlan(state, tracks, options)
		if err != nil {
			return "", false, err
		}
		if checked.PlanToken != options.PlanToken {
			return "", false, failure("tag_plan_stale", "the selected track identities changed; review the batch again")
		}
		seen := map[companion.Origin]bool{}
		for _, track := range tracks {
			if seen[track.Origin] {
				continue
			}
			seen[track.Origin] = true
			for _, id := range checked.Remove {
				if _, err := tx.ExecContext(ctx, "DELETE FROM Assignment WHERE originUUID=? AND originTrackId=? AND valueId=?", track.Origin.UUID, track.Origin.ID, id); err != nil {
					return "", false, err
				}
			}
			for _, id := range checked.Add {
				if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO Assignment VALUES(?,?,?)", track.Origin.UUID, track.Origin.ID, id); err != nil {
					return "", false, err
				}
			}
		}
		return "", checked.ChangedTracks > 0, nil
	})
	if err != nil {
		return nil, err
	}
	result.Applied, result.Revision = true, mutation.Revision
	return result, nil
}
