package playlist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const (
	importMatched      = "matched"
	importDuplicate    = "duplicate"
	importMissing      = "missing"
	importUnregistered = "unregistered"
	importAmbiguous    = "ambiguous"
)

// ImportOptions imports existing collection tracks into a new playlist only.
type ImportOptions struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Parent      string `json:"parent"`
	Encoding    string `json:"encoding"`
	SkipMissing bool   `json:"skip_missing"`
	DryRun      bool   `json:"dry_run"`
	PlanToken   string `json:"plan_token"`
}

// ImportEntry retains source order and explains every omitted membership.
type ImportEntry struct {
	Line       int    `json:"line"`
	Path       string `json:"path"`
	TrackID    int64  `json:"track_id,omitempty"`
	Status     string `json:"status"`
	OriginID   int64  `json:"-"`
	OriginUUID string `json:"-"`
}

// ImportPlan is a read-only review; its token binds the exact resolved inputs.
type ImportPlan struct {
	Path            string        `json:"path"`
	Name            string        `json:"name"`
	ParentID        int64         `json:"parent_id"`
	Encoding        string        `json:"encoding"`
	SkipMissing     bool          `json:"skip_missing"`
	Entries         []ImportEntry `json:"entries"`
	TrackIDs        []int64       `json:"track_ids"`
	DuplicateCount  int           `json:"duplicate_count"`
	UnresolvedCount int           `json:"unresolved_count"`
	CanApply        bool          `json:"can_apply"`
	PlanToken       string        `json:"plan_token"`
}

// ImportResult reports committed changes without a fallible post-commit entity read.
type ImportResult struct {
	PlaylistID     int64   `json:"playlist_id"`
	TrackIDs       []int64 `json:"track_ids"`
	ImportedCount  int     `json:"imported_count"`
	DuplicateCount int     `json:"duplicate_count"`
	SkippedCount   int     `json:"skipped_count"`
}

// PreviewImport observes the collection consistently without writing or creating history.
func PreviewImport(ctx context.Context, d *sql.DB, root string, opts ImportOptions) (*ImportPlan, error) {
	var err error
	opts, err = resolveImportParent(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return importPlan(ctx, tx, root, opts)
}

func importPlan(ctx context.Context, d db.Executor, root string, opts ImportOptions) (*ImportPlan, error) {
	source, err := readImport(ctx, opts)
	if err != nil {
		return nil, err
	}
	name, parentID, err := importDestination(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	byPath, err := importCollectionPaths(ctx, d, root)
	if err != nil {
		return nil, err
	}
	encoding := opts.Encoding
	if encoding == "" {
		encoding = m3uUTF8
	}
	plan := &ImportPlan{Path: source.path, Name: name, ParentID: parentID, Encoding: encoding, SkipMissing: opts.SkipMissing, Entries: source.entries, TrackIDs: []int64{}}
	matchImportEntries(plan, byPath)
	plan.PlanToken, err = importFingerprint(ctx, d, root, plan, source.bytes)
	return plan, err
}

func importDestination(ctx context.Context, d db.Executor, opts ImportOptions) (string, int64, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return "", 0, playlistFileError("playlist_import_target", "choose a name for the new playlist")
	}
	var parentID int64
	if opts.Parent != "" && opts.Parent != "0" {
		var err error
		parentID, err = strconv.ParseInt(opts.Parent, 10, 64)
		if err != nil || parentID <= 0 {
			return "", 0, playlistFileError("playlist_import_target", "invalid parent playlist")
		}
		var present int
		if err := d.QueryRowContext(ctx, "SELECT id FROM Playlist WHERE id=?", parentID).Scan(&present); err != nil {
			return "", 0, playlistFileError("playlist_import_target", "parent playlist no longer exists")
		}
	}
	var collision int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM Playlist WHERE parentListId=? AND title=?", parentID, name).Scan(&collision); err != nil {
		return "", 0, err
	}
	if collision != 0 {
		return "", 0, playlistFileError("playlist_import_target", "a playlist with this name already exists in the destination")
	}
	return name, parentID, nil
}

func importCollectionPaths(ctx context.Context, d db.Querier, root string) (map[string][]ImportEntry, error) {
	rows, err := d.QueryContext(ctx, "SELECT id,path,originTrackId,originDatabaseUuid FROM Track ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byPath := make(map[string][]ImportEntry)
	for rows.Next() {
		var tr ImportEntry
		var path sql.NullString
		if err := rows.Scan(&tr.TrackID, &path, &tr.OriginID, &tr.OriginUUID); err != nil {
			return nil, err
		}
		if !path.Valid || path.String == "" {
			continue
		}
		key := path.String
		if !filepath.IsAbs(key) {
			key = filepath.Join(root, key)
		}
		key = filepath.Clean(key)
		byPath[key] = append(byPath[key], tr)
	}
	return byPath, rows.Err()
}

func matchImportEntries(plan *ImportPlan, byPath map[string][]ImportEntry) {
	seen := make(map[int64]bool)
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		matches := byPath[entry.Path]
		info, statErr := os.Stat(entry.Path)
		switch {
		case statErr != nil || !info.Mode().IsRegular():
			entry.Status = importMissing
		case len(matches) == 0:
			entry.Status = importUnregistered
		case len(matches) > 1:
			entry.Status = importAmbiguous
		default:
			entry.TrackID = matches[0].TrackID
			entry.OriginID = matches[0].OriginID
			entry.OriginUUID = matches[0].OriginUUID
			entry.Status = importMatched
			if seen[entry.TrackID] {
				entry.Status = importDuplicate
				plan.DuplicateCount++
			} else {
				seen[entry.TrackID] = true
				plan.TrackIDs = append(plan.TrackIDs, entry.TrackID)
			}
		}
		if entry.Status != importMatched && entry.Status != importDuplicate {
			plan.UnresolvedCount++
		}
	}
	plan.CanApply = len(plan.TrackIDs) > 0 && (plan.SkipMissing || plan.UnresolvedCount == 0)
}

func importFingerprint(ctx context.Context, d db.Executor, root string, plan *ImportPlan, source []byte) (string, error) {
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", err
	}
	// Origins are part of the fingerprint even though they are not exposed as review copy.
	type origin struct {
		ID   int64
		UUID string
	}
	origins := make([]origin, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		origins = append(origins, origin{entry.OriginID, entry.OriginUUID})
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	fingerprint, err := json.Marshal(struct {
		Plan    *ImportPlan
		Source  []byte
		Library string
		Root    string
		Origins []origin
	}{plan, source, uuid, canonicalRoot, origins})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(fingerprint)
	return hex.EncodeToString(hash[:]), nil
}

// CheckImport rejects an invalid review before the caller creates safety snapshots.
func CheckImport(ctx context.Context, d *sql.DB, root string, opts ImportOptions) (*ImportPlan, error) {
	plan, err := PreviewImport(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	return plan, checkImportPlan(plan, opts.PlanToken)
}

func checkImportPlan(plan *ImportPlan, token string) error {
	if token == "" || token != plan.PlanToken {
		return playlistFileError("playlist_import_stale", "review the current playlist before importing")
	}
	if !plan.CanApply {
		return playlistFileError("playlist_import_unresolved", "resolve missing tracks or explicitly review skipping them")
	}
	return nil
}

// ApplyImport revalidates and commits the playlist and every ordered membership together.
func ApplyImport(ctx context.Context, d *sql.DB, root string, opts ImportOptions) (*ImportResult, error) {
	var err error
	opts, err = resolveImportParent(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := importPlan(ctx, tx, root, opts)
	if err != nil {
		return nil, err
	}
	if err := checkImportPlan(plan, opts.PlanToken); err != nil {
		return nil, err
	}
	id, err := db.CreatePlaylist(ctx, tx, plan.Name, plan.ParentID, false)
	if err != nil {
		return nil, err
	}
	for _, trackID := range plan.TrackIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := db.AddTrackToPlaylist(ctx, tx, id, trackID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ImportResult{PlaylistID: id, TrackIDs: plan.TrackIDs, ImportedCount: len(plan.TrackIDs), DuplicateCount: plan.DuplicateCount, SkippedCount: plan.UnresolvedCount}, nil
}

func resolveImportParent(ctx context.Context, d *sql.DB, opts ImportOptions) (ImportOptions, error) {
	if strings.HasPrefix(opts.Parent, "eng:") {
		parent, err := db.FindPlaylist(ctx, d, opts.Parent)
		if err != nil {
			return opts, err
		}
		if parent == nil {
			return opts, playlistFileError("playlist_import_target", "parent playlist no longer exists")
		}
		opts.Parent = strconv.FormatInt(parent.ID, 10)
	}
	return opts, nil
}
