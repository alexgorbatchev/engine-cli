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
	"regexp"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
)

// Earlier tag and artwork writes installed "<name>-upgraded-<digits>.<ext>"
// beside the original and pointed the track at it; repeated edits stacked the
// suffix. tagCopyName captures the original name and the stacked suffixes.
var tagCopyName = regexp.MustCompile(`^(.+?)((?:-upgraded-\d+)+)(\.[^.]+)$`)

// TagCopyFold is one track whose stored file is a tag-edit copy, and how the
// repair folds it back: the copy's bytes take the original name, and copies
// the track no longer needs are removed. Paths are stored paths, as the
// collection records them.
type TagCopyFold struct {
	TrackID int64  `json:"track_id"`
	Title   string `json:"title,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	// ReplacesOriginal is true when the original file still exists; it is
	// replaced by the copy, whose audio packets are identical.
	ReplacesOriginal bool     `json:"replaces_original"`
	Removes          []string `json:"removes,omitempty"`
}

// TagCopySkip is a track whose file looks like a tag-edit copy but is left
// alone, with a stable reason code.
type TagCopySkip struct {
	TrackID int64  `json:"track_id"`
	Title   string `json:"title,omitempty"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
}

// TagCopyPlan reviews the repair. Applying requires its plan token.
type TagCopyPlan struct {
	Folds     []TagCopyFold `json:"folds"`
	Skipped   []TagCopySkip `json:"skipped"`
	CanApply  bool          `json:"can_apply"`
	PlanToken string        `json:"plan_token"`
	files     []string
}

// TagCopyResult reports the applied repair from the plan it applied.
type TagCopyResult struct {
	TrackIDs []int64 `json:"track_ids"`
	Folded   int     `json:"folded"`
	Removed  int     `json:"removed"`
}

type tagCopyTrack struct {
	id            int64
	title, stored string
}

// PreviewTagCopies finds tracks stored as tag-edit copies. A copy folds back
// only when its compressed audio matches the original's, so a replacement from
// Upgrade audio, which shares the naming, is never folded.
func PreviewTagCopies(ctx context.Context, d *sql.DB, lib string, runner enginesync.CommandRunner) (*TagCopyPlan, error) {
	tracks, err := tagCopyTracks(ctx, d)
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	for _, t := range tracks {
		referenced[absStored(lib, t.stored)] = true
	}
	plan := &TagCopyPlan{Folds: []TagCopyFold{}, Skipped: []TagCopySkip{}}
	for _, t := range tracks {
		base := storedBase(t.stored)
		match := tagCopyName.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		skip := func(reason string) {
			plan.Skipped = append(plan.Skipped, TagCopySkip{TrackID: t.id, Title: t.title, Path: t.stored, Reason: reason})
		}
		copyPath := absStored(lib, t.stored)
		if _, err := os.Stat(copyPath); err != nil {
			skip("tag_copy_missing")
			continue
		}
		dir := t.stored[:len(t.stored)-len(base)]
		original := match[1] + match[3]
		originalPath := filepath.Join(filepath.Dir(copyPath), original)
		fold := TagCopyFold{TrackID: t.id, Title: t.title, From: t.stored, To: dir + original}
		if _, err := os.Stat(originalPath); err == nil {
			if referenced[originalPath] {
				skip("tag_copy_original_in_use")
				continue
			}
			if VerifyAudioPreserved(ctx, runner, originalPath, copyPath) != nil {
				skip("tag_copy_audio_differs")
				continue
			}
			fold.ReplacesOriginal = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		// Intermediate copies from stacked edits go too, when nothing else uses
		// them and they hold the same audio.
		suffixes := strings.SplitAfter(strings.TrimPrefix(match[2], "-upgraded-"), "-upgraded-")
		name := match[1]
		for _, suffix := range suffixes[:len(suffixes)-1] {
			name += "-upgraded-" + strings.TrimSuffix(suffix, "-upgraded-")
			path := filepath.Join(filepath.Dir(copyPath), name+match[3])
			if _, err := os.Stat(path); err != nil || referenced[path] || VerifyAudioPreserved(ctx, runner, path, copyPath) != nil {
				continue
			}
			fold.Removes = append(fold.Removes, dir+name+match[3])
		}
		plan.Folds = append(plan.Folds, fold)
		plan.files = append(plan.files, copyPath, originalPath)
		for _, removed := range fold.Removes {
			plan.files = append(plan.files, absStored(lib, removed))
		}
	}
	plan.CanApply = len(plan.Folds) > 0
	token, err := tagCopyToken(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanToken = token
	return plan, nil
}

// CheckTagCopies runs before Desktop checks, snapshots and history, so a stale
// or empty review is refused before anything is enrolled.
func CheckTagCopies(ctx context.Context, d *sql.DB, lib, token string, runner enginesync.CommandRunner) error {
	plan, err := PreviewTagCopies(ctx, d, lib, runner)
	if err != nil {
		return err
	}
	return checkTagCopyPlan(plan, token)
}

func checkTagCopyPlan(plan *TagCopyPlan, token string) error {
	if token != plan.PlanToken {
		return &progress.Error{Code: "tag_copies_stale", Cause: errors.New("review the tag-edit copies again before folding them")}
	}
	if !plan.CanApply {
		return &progress.Error{Code: "tag_copies_none", Cause: errors.New("no tag-edit copies to fold")}
	}
	return nil
}

// FoldTagCopies applies the reviewed plan. Every file it replaces or removes is
// preserved in history first, and the track rows commit after the files move,
// so one undo restores the copies, the originals and the stored paths.
func FoldTagCopies(ctx context.Context, d *sql.DB, lib, token string, runner enginesync.CommandRunner) (*TagCopyResult, error) {
	plan, err := PreviewTagCopies(ctx, d, lib, runner)
	if err != nil {
		return nil, err
	}
	if err := checkTagCopyPlan(plan, token); err != nil {
		return nil, err
	}
	result := &TagCopyResult{TrackIDs: []int64{}}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, fold := range plan.Folds {
		copyPath, originalPath := absStored(lib, fold.From), absStored(lib, fold.To)
		preserve := append([]string{copyPath, originalPath}, absAll(lib, fold.Removes)...)
		if err := history.Preserve(ctx, preserve...); err != nil {
			return result, err
		}
		info, err := os.Stat(copyPath)
		if err != nil {
			return result, err
		}
		res, err := tx.ExecContext(ctx, "UPDATE Track SET path=?,filename=?,fileBytes=?,lastEditTime=? WHERE id=? AND path IS ?", fold.To, storedBase(fold.To), info.Size(), now, fold.TrackID, fold.From)
		if err != nil {
			return result, err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return result, errors.Join(err, &progress.Error{Code: "tag_copies_stale", Cause: fmt.Errorf("track %d changed while folding", fold.TrackID)})
		}
		if fold.ReplacesOriginal {
			before, err := os.Stat(originalPath)
			if err != nil {
				return result, err
			}
			if err := publishInPlace(ctx, copyPath, originalPath, before); err != nil {
				return result, err
			}
			if err := os.Remove(copyPath); err != nil {
				return result, err
			}
		} else if err := os.Rename(copyPath, originalPath); err != nil {
			return result, err
		}
		for _, removed := range absAll(lib, fold.Removes) {
			if err := os.Remove(removed); err != nil {
				return result, err
			}
		}
		result.TrackIDs = append(result.TrackIDs, fold.TrackID)
		result.Folded++
		result.Removed += len(fold.Removes)
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func tagCopyTracks(ctx context.Context, d *sql.DB) ([]tagCopyTrack, error) {
	rows, err := d.QueryContext(ctx, "SELECT id,COALESCE(title,''),COALESCE(path,'') FROM Track ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tracks []tagCopyTrack
	for rows.Next() {
		var t tagCopyTrack
		if err := rows.Scan(&t.id, &t.title, &t.stored); err != nil {
			return nil, err
		}
		if t.stored != "" {
			tracks = append(tracks, t)
		}
	}
	return tracks, rows.Err()
}

// tagCopyToken binds the plan to the files it reviewed, so a file that
// changes after the review makes the apply refuse.
func tagCopyToken(plan *TagCopyPlan) (string, error) {
	type observed struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		MTime int64  `json:"mtime"`
	}
	var files []observed
	for _, path := range plan.files {
		file := observed{Path: path, Size: -1}
		if info, err := os.Stat(path); err == nil {
			file.Size, file.MTime = info.Size(), info.ModTime().UnixNano()
		}
		files = append(files, file)
	}
	data, err := json.Marshal(struct {
		Folds []TagCopyFold `json:"folds"`
		Files []observed    `json:"files"`
	}{plan.Folds, files})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func absStored(lib, stored string) string {
	path := filepath.FromSlash(stored)
	if !filepath.IsAbs(path) {
		path = filepath.Join(lib, path)
	}
	return filepath.Clean(path)
}

func absAll(lib string, stored []string) []string {
	paths := make([]string, len(stored))
	for i, value := range stored {
		paths[i] = absStored(lib, value)
	}
	return paths
}

// storedBase returns the file name of a stored path, which Engine records with
// forward slashes on every platform.
func storedBase(stored string) string {
	return stored[strings.LastIndexAny(stored, `/\`)+1:]
}
