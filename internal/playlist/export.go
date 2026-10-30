package playlist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const (
	exportReady       = "ready"
	exportUnavailable = "unavailable"
	exportAbsolute    = "absolute"
	exportRelative    = "relative"
)

// ExportOptions creates a new server-local file without modifying the source collection.
type ExportOptions struct {
	Identifier      string `json:"identifier"`
	Path            string `json:"path"`
	Encoding        string `json:"encoding"`
	PathMode        string `json:"path_mode"`
	SkipUnavailable bool   `json:"skip_unavailable"`
	DryRun          bool   `json:"dry_run"`
	PlanToken       string `json:"plan_token"`
}

// ExportEntry describes one saved membership, including unavailable media.
type ExportEntry struct {
	EntityID int64  `json:"entity_id"`
	TrackID  int64  `json:"track_id,omitempty"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	Duration int64  `json:"duration"`
	Status   string `json:"status"`
}

// ExportPlan binds ordered output and omission policy to a read-only source observation.
type ExportPlan struct {
	PlaylistID       int64         `json:"playlist_id"`
	PlaylistTitle    string        `json:"playlist_title"`
	Path             string        `json:"path"`
	Encoding         string        `json:"encoding"`
	PathMode         string        `json:"path_mode"`
	SkipUnavailable  bool          `json:"skip_unavailable"`
	Entries          []ExportEntry `json:"entries"`
	ExportedCount    int           `json:"exported_count"`
	UnavailableCount int           `json:"unavailable_count"`
	CanApply         bool          `json:"can_apply"`
	PlanToken        string        `json:"plan_token"`
}

// ExportResult reports a completed file; the source has no snapshot or undo entry.
type ExportResult struct {
	PlaylistID    int64  `json:"playlist_id"`
	Path          string `json:"path"`
	Encoding      string `json:"encoding"`
	PathMode      string `json:"path_mode"`
	ExportedCount int    `json:"exported_count"`
	SkippedCount  int    `json:"skipped_count"`
}

type exportReview struct {
	plan   *ExportPlan
	output []byte
}

// PreviewExport reviews direct memberships in saved order without creating a destination.
func PreviewExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*ExportPlan, error) {
	review, err := reviewExport(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	return review.plan, nil
}

func reviewExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*exportReview, error) {
	opts, err := normalizeExport(opts)
	if err != nil {
		return nil, err
	}
	pl, err := db.FindPlaylist(ctx, d, opts.Identifier)
	if err != nil {
		return nil, err
	}
	if pl == nil {
		return nil, playlistFileError("playlist_export_target", "choose an existing playlist")
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Re-read the title in the same observation as the memberships and library UUID.
	if err := tx.QueryRowContext(ctx, "SELECT title FROM Playlist WHERE id=?", pl.ID).Scan(&pl.Title); err != nil {
		return nil, err
	}
	return buildExportReview(ctx, tx, root, pl, opts)
}

func buildExportReview(ctx context.Context, d db.Executor, root string, pl *db.Playlist, opts ExportOptions) (*exportReview, error) {
	members, err := readExportMembers(ctx, d, pl.ID)
	if err != nil {
		return nil, err
	}
	plan := &ExportPlan{PlaylistID: pl.ID, PlaylistTitle: pl.Title, Path: opts.Path, Encoding: opts.Encoding, PathMode: opts.PathMode, SkipUnavailable: opts.SkipUnavailable, Entries: []ExportEntry{}}
	for _, member := range members {
		entry, err := prepareExportEntry(root, opts, member)
		if err != nil {
			return nil, err
		}
		plan.Entries = append(plan.Entries, entry)
		if entry.Status == exportReady {
			plan.ExportedCount++
		} else {
			plan.UnavailableCount++
		}
	}
	plan.CanApply = plan.UnavailableCount == 0 || opts.SkipUnavailable
	output, err := serializeExport(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanToken, err = exportFingerprint(ctx, d, root, plan, members, output)
	if err != nil {
		return nil, err
	}
	return &exportReview{plan: plan, output: output}, nil
}

func exportFingerprint(ctx context.Context, d db.Executor, root string, plan *ExportPlan, members []exportMember, output []byte) (string, error) {
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Plan       *ExportPlan
		Members    []exportMember
		Output     []byte
		UUID, Root string
	}{plan, members, output, uuid, canonical})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// ApplyExport revalidates the exact review, then exclusively creates its new output file.
func ApplyExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*ExportResult, error) {
	review, err := reviewExport(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	if opts.PlanToken == "" || opts.PlanToken != review.plan.PlanToken {
		return nil, playlistFileError("playlist_export_stale", "review the current export before saving")
	}
	if !review.plan.CanApply {
		return nil, playlistFileError("playlist_export_unavailable", "resolve unavailable media or explicitly review skipping it")
	}
	if err := publishExport(ctx, review.plan.Path, review.output); err != nil {
		return nil, err
	}
	plan := review.plan
	return &ExportResult{PlaylistID: plan.PlaylistID, Path: plan.Path, Encoding: plan.Encoding, PathMode: plan.PathMode, ExportedCount: plan.ExportedCount, SkippedCount: plan.UnavailableCount}, nil
}
