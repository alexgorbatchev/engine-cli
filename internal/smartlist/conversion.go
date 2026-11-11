package smartlist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type ConversionPlan struct {
	UUID       string `json:"uuid"`
	Title      string `json:"title"`
	ParentID   int64  `json:"parent_id"`
	ParentPath string `json:"parent_path"`
	TrackCount int    `json:"track_count"`
	CanApply   bool   `json:"can_apply"`
	PlanToken  string `json:"plan_token"`
}

type ConversionOptions struct {
	UUID      string `json:"uuid"`
	PlanToken string `json:"plan_token"`
}

type ConversionResult struct {
	UUID       string `json:"uuid"`
	PlaylistID int64  `json:"playlist_id"`
	Title      string `json:"title"`
	TrackCount int    `json:"track_count"`
}

type conversion struct {
	plan      ConversionPlan
	ids       []int64
	inventory *db.SmartlistInventory
}

// PreviewConversion observes one snapshot, including the destination name and
// membership. Applying always recomputes this plan before the first write.
func PreviewConversion(ctx context.Context, d *sql.DB, id, libraryFolder string) (*ConversionPlan, error) {
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := planConversion(ctx, tx, id, libraryFolder)
	if err != nil {
		return nil, err
	}
	return &c.plan, nil
}

func planConversion(ctx context.Context, d db.Reader, id, libraryFolder string) (*conversion, error) {
	name, raw, err := read(ctx, d, id)
	if err != nil {
		return nil, err
	}
	name, err = title(name)
	if err != nil {
		return nil, err
	}
	inventory, err := db.ReadSmartlistInventoryFrom(ctx, d)
	if err != nil {
		return nil, err
	}
	if len(inventory.Issues) > 0 {
		return nil, failure("smartlist_paths_damaged")
	}
	c := &conversion{plan: ConversionPlan{UUID: id}, inventory: inventory}
	for _, row := range inventory.Saved {
		if row.UUID == id {
			c.plan.ParentPath = row.ParentPath
			break
		}
	}
	for _, row := range inventory.Playlists {
		if row.Path == c.plan.ParentPath {
			c.plan.ParentID = row.ID
			break
		}
	}
	c.plan.Title = conversionName(name, c.plan.ParentPath, inventory.Playlists)
	c.ids, err = MatchingIDs(ctx, d, id, libraryFolder)
	if err != nil {
		return nil, err
	}
	c.plan.TrackCount = len(c.ids)
	c.plan.CanApply = len(c.ids) > 0
	encoded, err := json.Marshal(struct {
		Plan  ConversionPlan
		Rules string
		IDs   []int64
	}{c.plan, raw, c.ids})
	if err != nil {
		return nil, err
	}
	c.plan.PlanToken = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return c, nil
}

func conversionName(name, parentPath string, playlists []db.SmartlistPlaylist) string {
	paths := make(map[string]bool, len(playlists))
	for _, playlist := range playlists {
		paths[playlist.Path] = true
	}
	for suffix := 1; ; suffix++ {
		candidate := name
		if suffix > 1 {
			candidate = fmt.Sprintf("%s %d", name, suffix)
		}
		// Semicolons in titles can alias paths at another hierarchy depth.
		if !paths[parentPath+candidate+";"] {
			return candidate
		}
	}
}

func Convert(ctx context.Context, d *sql.DB, libraryFolder string, opts ConversionOptions) (*ConversionResult, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := planConversion(ctx, tx, opts.UUID, libraryFolder)
	if err != nil {
		return nil, err
	}
	if opts.PlanToken == "" || opts.PlanToken != c.plan.PlanToken {
		return nil, failure("smartlist_conversion_changed")
	}
	if !c.plan.CanApply {
		return nil, failure("smartlist_conversion_empty")
	}
	id, err := db.CreatePlaylist(ctx, tx, c.plan.Title, c.plan.ParentID, false)
	if err != nil {
		return nil, err
	}
	// Ordinary insert triggers only maintain the ordinary sibling chain. Move
	// terminal Smartlist links before the new ordinary tail without editing rules.
	for _, row := range c.inventory.Saved {
		if row.ParentPath == c.plan.ParentPath && row.NextPath == "" && row.NextUUID == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE Smartlist SET nextPlaylistPath=?,nextListUuid='' WHERE listUuid=?`, c.plan.ParentPath+c.plan.Title+";", row.UUID); err != nil {
				return nil, err
			}
		}
	}
	for _, track := range c.ids {
		if _, err := db.AddTrackToPlaylist(ctx, tx, id, track); err != nil {
			return nil, err
		}
	}
	final, err := db.ReadSmartlistInventoryFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(final.Issues) > 0 {
		return nil, failure("smartlist_paths_damaged")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ConversionResult{UUID: opts.UUID, PlaylistID: id, Title: c.plan.Title, TrackCount: len(c.ids)}, nil
}
