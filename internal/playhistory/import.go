package playhistory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

const importDuplicate = "duplicate"

// ImportOptions creates a new playlist from one played session.
type ImportOptions struct {
	Session int64 `json:"session"`
	// Name defaults to the session's title, numbered like Engine's import when taken.
	Name        string `json:"name"`
	Parent      string `json:"parent"`
	SkipMissing bool   `json:"skip_missing"`
	DryRun      bool   `json:"dry_run"`
	PlanToken   string `json:"plan_token"`
}

// Validate rejects options that cannot name a session.
func (o ImportOptions) Validate() error {
	if o.Session <= 0 {
		return fmt.Errorf("session must be a positive history session ID")
	}
	return nil
}

// ImportPlan is a read-only review; its token binds the exact session, options, and collection.
type ImportPlan struct {
	Session        int64   `json:"session"`
	SessionTitle   string  `json:"session_title"`
	Name           string  `json:"name"`
	ParentID       int64   `json:"parent_id"`
	SkipMissing    bool    `json:"skip_missing"`
	Entries        []Entry `json:"entries"`
	TrackIDs       []int64 `json:"track_ids"`
	DuplicateCount int     `json:"duplicate_count"`
	MissingCount   int     `json:"missing_count"`
	CanApply       bool    `json:"can_apply"`
	PlanToken      string  `json:"plan_token"`
}

// ImportResult reports the committed playlist.
type ImportResult struct {
	PlaylistID     int64   `json:"playlist_id"`
	Name           string  `json:"name"`
	TrackIDs       []int64 `json:"track_ids"`
	ImportedCount  int     `json:"imported_count"`
	DuplicateCount int     `json:"duplicate_count"`
	SkippedCount   int     `json:"skipped_count"`
}

// PreviewImport reviews a session's import without writing either database.
func PreviewImport(ctx context.Context, libraryDir string, library *sql.DB, opts ImportOptions) (*ImportPlan, error) {
	opts, err := resolveParent(ctx, library, opts)
	if err != nil {
		return nil, err
	}
	tx, err := library.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return planImport(ctx, libraryDir, tx, opts)
}

// CheckImport rejects a stale or unresolved review before snapshots are taken.
func CheckImport(ctx context.Context, libraryDir string, library *sql.DB, opts ImportOptions) error {
	plan, err := PreviewImport(ctx, libraryDir, library, opts)
	if err != nil {
		return err
	}
	return checkPlan(plan, opts.PlanToken)
}

// ApplyImport revalidates the review and commits the playlist with its tracks in play order.
func ApplyImport(ctx context.Context, libraryDir string, library *sql.DB, opts ImportOptions) (*ImportResult, error) {
	opts, err := resolveParent(ctx, library, opts)
	if err != nil {
		return nil, err
	}
	tx, err := library.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := planImport(ctx, libraryDir, tx, opts)
	if err != nil {
		return nil, err
	}
	if err := checkPlan(plan, opts.PlanToken); err != nil {
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
	return &ImportResult{PlaylistID: id, Name: plan.Name, TrackIDs: plan.TrackIDs, ImportedCount: len(plan.TrackIDs), DuplicateCount: plan.DuplicateCount, SkippedCount: plan.MissingCount}, nil
}

func checkPlan(plan *ImportPlan, token string) error {
	if token == "" || token != plan.PlanToken {
		return historyError("session_import_stale", "review the played session again before creating its playlist")
	}
	if !plan.CanApply {
		return historyError("session_import_unresolved", "review skipping tracks that are no longer in the library")
	}
	return nil
}

func planImport(ctx context.Context, libraryDir string, library db.Executor, opts ImportOptions) (*ImportPlan, error) {
	if err := opts.Validate(); err != nil {
		return nil, historyError("session_not_found", err.Error())
	}
	history, err := Open(libraryDir)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return nil, historyError("play_history_unavailable", "this library has no play history yet")
	}
	defer history.Close()
	tracks, err := readLibraryTracks(ctx, library)
	if err != nil {
		return nil, err
	}
	// One read transaction keeps the session and its plays consistent with each other.
	read, err := history.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, unreadable(err)
	}
	defer read.Rollback()
	session, err := inspect(ctx, read, tracks, opts.Session)
	if err != nil {
		return nil, err
	}
	parentID, err := importParent(ctx, library, opts.Parent)
	if err != nil {
		return nil, err
	}
	name, err := importName(ctx, library, opts.Name, session.Title, parentID)
	if err != nil {
		return nil, err
	}
	plan := &ImportPlan{Session: session.ID, SessionTitle: session.Title, Name: name, ParentID: parentID, SkipMissing: opts.SkipMissing, Entries: session.Entries, TrackIDs: []int64{}}
	seen := map[int64]bool{}
	// Playlists hold each track once, so repeated plays keep their first position.
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		switch {
		case entry.Status == statusMissing:
			plan.MissingCount++
		case seen[entry.TrackID]:
			entry.Status = importDuplicate
			plan.DuplicateCount++
		default:
			seen[entry.TrackID] = true
			plan.TrackIDs = append(plan.TrackIDs, entry.TrackID)
		}
	}
	plan.CanApply = len(plan.TrackIDs) > 0 && (plan.SkipMissing || plan.MissingCount == 0)
	plan.PlanToken, err = fingerprint(ctx, library, plan)
	return plan, err
}

func importParent(ctx context.Context, library db.Executor, parent string) (int64, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" || parent == "0" {
		return 0, nil
	}
	id, err := strconv.ParseInt(parent, 10, 64)
	if err != nil || id <= 0 {
		return 0, historyError("session_import_target", "invalid parent playlist")
	}
	var present int64
	if err := library.QueryRowContext(ctx, "SELECT id FROM Playlist WHERE id = ?", id).Scan(&present); err != nil {
		if err == sql.ErrNoRows {
			return 0, historyError("session_import_target", "the parent playlist no longer exists")
		}
		return 0, err
	}
	return id, nil
}

// importName keeps an explicit name exact, refusing one that is taken. The
// default session title is numbered "Title 2", "Title 3"… as Engine does.
func importName(ctx context.Context, library db.Executor, name, sessionTitle string, parentID int64) (string, error) {
	explicit := strings.TrimSpace(name)
	base := explicit
	if base == "" {
		base = strings.TrimSpace(sessionTitle)
	}
	for n := 1; ; n++ {
		candidate := base
		if n > 1 {
			candidate = fmt.Sprintf("%s %d", base, n)
		}
		var taken int
		if err := library.QueryRowContext(ctx, "SELECT COUNT(*) FROM Playlist WHERE parentListId = ? AND title = ?", parentID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
		if explicit != "" {
			return "", historyError("session_import_target", "a playlist with this name already exists in the destination")
		}
	}
}

// resolveParent turns an opaque parent reference into its numeric ID before the review reads.
func resolveParent(ctx context.Context, library *sql.DB, opts ImportOptions) (ImportOptions, error) {
	if !strings.HasPrefix(opts.Parent, "eng:") {
		return opts, nil
	}
	found, err := db.FindPlaylist(ctx, library, opts.Parent)
	if err := ctx.Err(); err != nil {
		return opts, err
	}
	// Malformed, foreign, and ambiguous references all fail to name a destination here.
	if err != nil || found == nil {
		return opts, historyError("session_import_target", "the parent playlist no longer exists")
	}
	opts.Parent = strconv.FormatInt(found.ID, 10)
	return opts, nil
}

func fingerprint(ctx context.Context, library db.Executor, plan *ImportPlan) (string, error) {
	var uuid string
	if err := library.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id = 1").Scan(&uuid); err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Plan    *ImportPlan
		Library string
	}{plan, uuid})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
