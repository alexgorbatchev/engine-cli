package playlist

import (
	"context"
	"database/sql"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// ReorderOptions describes a move in the displayed native ascending or
// descending order. View contains library-local Track IDs; Rows indexes View,
// and Drop identifies a gap before a row, including the gap after the last row.
type ReorderOptions struct {
	Playlist   string  `json:"playlist"`
	View       []int64 `json:"view"`
	Rows       []int   `json:"rows"`
	Drop       int     `json:"drop"`
	Descending bool    `json:"descending"`
	DryRun     bool    `json:"dry_run"`
	PlanToken  string  `json:"plan_token"`
}

// ReorderPlan reports the resolved track order without exposing missing-origin
// memberships as selectable tracks. Their native entities remain in the plan.
type ReorderPlan struct {
	PlaylistID int64   `json:"playlist_id"`
	TrackIDs   []int64 `json:"track_ids"`
	CanApply   bool    `json:"can_apply"`
	PlanToken  string  `json:"plan_token"`
	entities   []int64
	members    []orderMember
}

type orderMember struct {
	ID        int64
	Next      sql.NullInt64
	OriginID  sql.NullInt64
	Origin    sql.NullString
	Reference sql.NullInt64
	TrackID   sql.NullInt64
}

// ReorderResult reports only values held before the commit.
type ReorderResult struct {
	PlaylistID int64   `json:"playlist_id"`
	TrackIDs   []int64 `json:"track_ids"`
}

// PreviewReorder observes a consistent membership snapshot without writing.
func PreviewReorder(ctx context.Context, d *sql.DB, root string, opts ReorderOptions) (*ReorderPlan, error) {
	id, err := reorderPlaylistID(ctx, d, opts.Playlist)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return planReorder(ctx, tx, root, id, opts)
}

// CheckReorder rejects stale or ineffective reviews before safety artifacts.
func CheckReorder(ctx context.Context, d *sql.DB, root string, opts ReorderOptions) (*ReorderPlan, error) {
	plan, err := PreviewReorder(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	if err := checkReorder(plan, opts.PlanToken); err != nil {
		return nil, err
	}
	return plan, nil
}

func checkReorder(plan *ReorderPlan, token string) error {
	if token == "" || token != plan.PlanToken {
		return playlistFileError("playlist_reorder_stale", "review the current playlist order again")
	}
	if !plan.CanApply {
		return playlistFileError("playlist_reorder_unchanged", "choose a different position for the selected tracks")
	}
	return nil
}

func reorderPlaylistID(ctx context.Context, d *sql.DB, selector string) (int64, error) {
	p, err := db.FindPlaylist(ctx, d, selector)
	if err != nil {
		return 0, err
	}
	if p == nil {
		return 0, playlistFileError("playlist_reorder_target", "playlist no longer exists")
	}
	return p.ID, nil
}

func planReorder(ctx context.Context, tx *sql.Tx, root string, id int64, opts ReorderOptions) (*ReorderPlan, error) {
	members, err := reorderMembers(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	chain := make([]int64, 0, len(members))
	byTrack := make(map[int64]int64, len(members))
	for _, m := range members {
		chain = append(chain, m.ID)
		if m.TrackID.Valid {
			byTrack[m.TrackID.Int64] = m.ID
		}
	}
	view := make([]int64, 0, len(opts.View))
	if opts.View == nil {
		for _, m := range members {
			if m.TrackID.Valid {
				view = append(view, m.ID)
			}
		}
		if opts.Descending {
			slices.Reverse(view)
		}
	} else {
		for _, track := range opts.View {
			entity, ok := byTrack[track]
			if !ok {
				return nil, playlistFileError("playlist_reorder_view", "a shown track is no longer a member of this playlist")
			}
			view = append(view, entity)
		}
	}
	move := orderMove{view: view, rows: opts.Rows, drop: opts.Drop, descending: opts.Descending}
	ordered, err := moveOrder(chain, move)
	if err != nil {
		return nil, playlistFileError("playlist_reorder_view", "shown order or selection no longer matches the playlist")
	}
	plan := &ReorderPlan{PlaylistID: id, CanApply: !slices.Equal(chain, ordered), entities: ordered, members: members, TrackIDs: []int64{}}
	byEntity := make(map[int64]orderMember, len(members))
	for _, m := range members {
		byEntity[m.ID] = m
	}
	for _, entity := range ordered {
		if track := byEntity[entity].TrackID; track.Valid {
			plan.TrackIDs = append(plan.TrackIDs, track.Int64)
		}
	}
	plan.PlanToken, err = reorderFingerprint(ctx, tx, root, plan, opts)
	return plan, err
}
