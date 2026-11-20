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
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// SharedLookup reports the other known libraries that reference a file and
// those it could not read, by path.
type SharedLookup func(ctx context.Context, file string) (shared, unavailable []string, err error)

// MovePlan reviews moving one track's audio file: where it is and where it
// goes, as stored paths and absolute paths, and every reason it cannot move
// now. Applying requires its token.
type MovePlan struct {
	TrackID     int64    `json:"track_id"`
	Title       string   `json:"title,omitempty"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Source      string   `json:"source"`
	Target      string   `json:"target"`
	Conflicts   []string `json:"conflicts"`
	SharedWith  []string `json:"shared_with"`
	Unavailable []string `json:"unavailable"`
	CanApply    bool     `json:"can_apply"`
	PlanToken   string   `json:"plan_token"`
	observed    string
}

// PlanMove reviews moving a track's audio file to destination, a file path or
// a folder (an existing folder, or a path ending in a separator). A relative
// destination is inside the library folder, as stored paths are. It reads
// only. Conflicts: move_unchanged (the file is already there),
// move_source_missing, move_target_exists (another file has that name),
// move_shared_file (another known library uses the file) and
// move_shared_unknown (a known library could not be read to check).
func PlanMove(ctx context.Context, d *sql.DB, lib, identifier, destination string, shared SharedLookup) (*MovePlan, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, fmt.Errorf("destination path cannot be empty")
	}
	t, err := db.FindTrack(ctx, d, identifier)
	if err != nil {
		return nil, fmt.Errorf("finding track %q: %w", identifier, err)
	}
	if t == nil {
		return nil, &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q not found in collection", identifier)}
	}
	source := t.PathString()
	if !filepath.IsAbs(source) {
		source = filepath.Join(lib, source)
	}
	target := moveTarget(lib, source, destination)
	stored := target
	if rel, err := filepath.Rel(lib, target); err == nil && filepath.IsLocal(rel) {
		stored = filepath.ToSlash(rel)
	}
	plan := &MovePlan{TrackID: t.ID, Title: t.Title.String, From: t.PathString(), To: stored, Source: source, Target: target, Conflicts: []string{}, SharedWith: []string{}, Unavailable: []string{}}
	info, err := os.Stat(source)
	switch {
	case os.IsNotExist(err):
		plan.Conflicts = append(plan.Conflicts, "move_source_missing")
	case err != nil:
		return nil, err
	default:
		plan.observed = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	if filepath.Clean(source) == filepath.Clean(target) {
		plan.Conflicts = append(plan.Conflicts, "move_unchanged")
	} else if other, err := os.Stat(target); err == nil && (info == nil || !os.SameFile(info, other)) {
		plan.Conflicts = append(plan.Conflicts, "move_target_exists")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if shared != nil && info != nil {
		plan.SharedWith, plan.Unavailable, err = shared(ctx, source)
		if err != nil {
			return nil, err
		}
		if len(plan.SharedWith) > 0 {
			plan.Conflicts = append(plan.Conflicts, "move_shared_file")
		}
		if len(plan.Unavailable) > 0 {
			plan.Conflicts = append(plan.Conflicts, "move_shared_unknown")
		}
	}
	plan.CanApply = len(plan.Conflicts) == 0
	data, err := json.Marshal(struct {
		Plan     *MovePlan `json:"plan"`
		Observed string    `json:"observed"`
	}{plan, plan.observed})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	plan.PlanToken = hex.EncodeToString(sum[:])
	return plan, nil
}

// CheckMove refuses a stale review or one with conflicts before Desktop
// checks, snapshots and history are enrolled.
func CheckMove(ctx context.Context, d *sql.DB, lib, identifier, destination, token string, shared SharedLookup) error {
	plan, err := PlanMove(ctx, d, lib, identifier, destination, shared)
	if err != nil {
		return err
	}
	if token != plan.PlanToken {
		return &progress.Error{Code: "move_plan_stale", Cause: errors.New("the track or its files changed since the move was reviewed")}
	}
	if !plan.CanApply {
		return &progress.Error{Code: plan.Conflicts[0], Cause: fmt.Errorf("the reviewed move cannot apply: %s", strings.Join(plan.Conflicts, ", "))}
	}
	return nil
}

// moveTarget resolves where a move puts source: into destination when it is
// an existing folder or ends in a separator, else at destination itself. A
// relative destination resolves against the library folder, like the stored
// paths a client reads from track listings, never against the process's own
// working directory.
func moveTarget(lib, source, destination string) string {
	folder := strings.HasSuffix(destination, "/") || strings.HasSuffix(destination, "\\")
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(lib, destination)
	}
	if info, err := os.Stat(destination); folder || (err == nil && info.IsDir()) {
		return filepath.Join(destination, filepath.Base(source))
	}
	return filepath.Clean(destination)
}
