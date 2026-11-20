package track

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// MaxArtworkImports is the most tracks one reviewed cover import plans or
// applies.
const MaxArtworkImports = 200

// ArtworkImportTrack is one track's planned cover from its own audio file,
// or why it has none to use.
type ArtworkImportTrack struct {
	Identifier string `json:"identifier"`
	TrackID    int64  `json:"track_id,omitempty"`
	Title      string `json:"title,omitempty"`
	// Replaces is true when the library already shows another cover for the
	// track; Unchanged when the library cover already is the file's cover.
	Replaces  bool   `json:"replaces"`
	Unchanged bool   `json:"unchanged"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	observed  string
	prepared  *artwork.Prepared
}

// ArtworkImportPlan reviews setting each track's library cover from the art
// embedded in its audio file. Applying requires its token.
type ArtworkImportPlan struct {
	Tracks    []ArtworkImportTrack `json:"tracks"`
	Changed   int                  `json:"changed"`
	Replaced  int                  `json:"replaced"`
	CanApply  bool                 `json:"can_apply"`
	PlanToken string               `json:"plan_token"`
}

// ArtworkImportResult reports the tracks whose library cover changed.
type ArtworkImportResult struct {
	TrackIDs []int64 `json:"track_ids"`
	Changed  int     `json:"changed"`
}

// ArtworkImportOptions configures reviews and imports.
type ArtworkImportOptions struct {
	Library string
	Runner  func(context.Context, string, ...string) ([]byte, error)
}

// ValidateArtworkImport checks the request before any file is read.
func ValidateArtworkImport(identifiers []string) error {
	if len(identifiers) == 0 {
		return fmt.Errorf("identifiers must name at least one track")
	}
	if len(identifiers) > MaxArtworkImports {
		return &progress.Error{Code: "batch_limit_exceeded", Cause: fmt.Errorf("identifiers accepts at most %d tracks", MaxArtworkImports)}
	}
	seen := map[string]bool{}
	for _, identifier := range identifiers {
		if strings.TrimSpace(identifier) == "" {
			return fmt.Errorf("identifiers cannot contain an empty value")
		}
		if seen[identifier] {
			return fmt.Errorf("identifiers repeats %q", identifier)
		}
		seen[identifier] = true
	}
	return nil
}

// PreviewArtworkImport reads each track's audio file, without writing it, and
// plans the library cover its embedded art would become. A track whose file
// has no art, or is missing, is reported and left out.
func PreviewArtworkImport(ctx context.Context, d *sql.DB, identifiers []string, opts ArtworkImportOptions) (*ArtworkImportPlan, error) {
	if err := ValidateArtworkImport(identifiers); err != nil {
		return nil, err
	}
	plan := &ArtworkImportPlan{Tracks: make([]ArtworkImportTrack, 0, len(identifiers))}
	for i, identifier := range identifiers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := ArtworkImportTrack{Identifier: identifier}
		if err := planArtworkImport(ctx, d, opts, &item); err != nil {
			item.Error, item.ErrorCode = err.Error(), progress.ErrorCode(err)
		}
		if item.prepared != nil && !item.Unchanged {
			plan.Changed++
			if item.Replaces {
				plan.Replaced++
			}
		}
		plan.Tracks = append(plan.Tracks, item)
		progress.Item(ctx, "track.artwork.import", i+1, len(identifiers))
	}
	plan.CanApply = plan.Changed > 0
	token, err := artworkImportToken(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanToken = token
	return plan, nil
}

func planArtworkImport(ctx context.Context, d *sql.DB, opts ArtworkImportOptions, item *ArtworkImportTrack) error {
	t, err := db.FindTrack(ctx, d, item.Identifier)
	if err != nil {
		return err
	}
	if t == nil {
		return &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q not found", item.Identifier)}
	}
	item.TrackID, item.Title = t.ID, t.Title.String
	path, err := originalUpgradePath(t, opts.Library)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &progress.Error{Code: "file_not_found", Cause: err}
		}
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	probe, err := probeUpgrade(ctx, opts.Runner, path)
	if err != nil {
		return err
	}
	prepared, err := taggedArtwork(ctx, opts.Runner, path, probe)
	if err != nil {
		return err
	}
	if prepared == nil {
		return &progress.Error{Code: "file_no_artwork", Cause: errors.New("the audio file has no embedded cover")}
	}
	item.prepared = prepared
	item.Unchanged = t.AlbumArtSourceHash.Valid && t.AlbumArtSourceHash.String == prepared.Hash
	item.Replaces = !item.Unchanged && t.AlbumArtID.Valid
	current := ""
	if t.AlbumArtSourceHash.Valid {
		current = t.AlbumArtSourceHash.String
	}
	item.observed = fmt.Sprintf("%s:%d:%d:%s:%s", path, info.Size(), info.ModTime().UnixNano(), current, prepared.Hash)
	return nil
}

// artworkImportToken binds the plan to each file as it was read and to the
// library cover it replaces, so a change to either makes the import refuse.
func artworkImportToken(plan *ArtworkImportPlan) (string, error) {
	type bound struct {
		Identifier string `json:"identifier"`
		Observed   string `json:"observed"`
		ErrorCode  string `json:"error_code"`
	}
	tracks := make([]bound, len(plan.Tracks))
	for i, item := range plan.Tracks {
		tracks[i] = bound{item.Identifier, item.observed, item.ErrorCode}
	}
	data, err := json.Marshal(tracks)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// CheckArtworkImport refuses a stale or empty review before Desktop checks,
// snapshots and history are enrolled.
func CheckArtworkImport(ctx context.Context, d *sql.DB, identifiers []string, token string, opts ArtworkImportOptions) error {
	plan, err := PreviewArtworkImport(ctx, d, identifiers, opts)
	if err != nil {
		return err
	}
	return checkArtworkImportPlan(plan, token)
}

func checkArtworkImportPlan(plan *ArtworkImportPlan, token string) error {
	if token == "" {
		return fmt.Errorf("plan_token is required; review the import with dry_run first")
	}
	if token != plan.PlanToken {
		return &progress.Error{Code: "artwork_import_plan_stale", Cause: errors.New("a file or cover changed since review; review the import again")}
	}
	if !plan.CanApply {
		return &progress.Error{Code: "artwork_import_unchanged", Cause: errors.New("the library already shows these covers")}
	}
	return nil
}

// ApplyArtworkImport plans again, refuses a token that no longer matches, and
// sets each planned track's library cover from its file in one transaction.
// The audio files are only read; their bytes never change.
func ApplyArtworkImport(ctx context.Context, d *sql.DB, identifiers []string, token string, opts ArtworkImportOptions) (*ArtworkImportResult, error) {
	plan, err := PreviewArtworkImport(ctx, d, identifiers, opts)
	if err != nil {
		return nil, err
	}
	if err := checkArtworkImportPlan(plan, token); err != nil {
		return nil, err
	}
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := &ArtworkImportResult{TrackIDs: []int64{}}
	for _, item := range plan.Tracks {
		if item.prepared == nil || item.Unchanged {
			continue
		}
		id, err := item.prepared.Store(ctx, tx, lib)
		if err != nil {
			return nil, err
		}
		update := metadataUpdate{
			ID:      item.TrackID,
			Columns: []string{"albumArtId = ?", "albumArt = ?", "albumArtSourceHash = ?"},
			Values:  []any{id, fmt.Sprintf("image://planck/%d", id), item.prepared.Hash},
		}
		if err := saveMetadata(ctx, tx, update); err != nil {
			return nil, err
		}
		result.TrackIDs = append(result.TrackIDs, item.TrackID)
		progress.Item(ctx, "track.artwork.import", len(result.TrackIDs), plan.Changed)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	result.Changed = len(result.TrackIDs)
	return result, nil
}
