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

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// MaxTagEdits is the most files one reviewed tag edit plans or writes.
const MaxTagEdits = 200

// TagEditFields lists the embedded fields a reviewed edit can set, in the
// order reviews report them.
var TagEditFields = []string{"title", "artist", "album", "genre", "date", "comment"}

// TagEdit is one file's requested values. A nil field is left alone; an empty
// string clears it. Each file carries its own values, so one Title is never
// written to several files.
type TagEdit struct {
	Identifier string  `json:"identifier"`
	Title      *string `json:"title,omitempty"`
	Artist     *string `json:"artist,omitempty"`
	Album      *string `json:"album,omitempty"`
	Genre      *string `json:"genre,omitempty"`
	Date       *string `json:"date,omitempty"`
	Comment    *string `json:"comment,omitempty"`
}

func (e TagEdit) value(field string) *string {
	switch field {
	case "title":
		return e.Title
	case "artist":
		return e.Artist
	case "album":
		return e.Album
	case "genre":
		return e.Genre
	case "date":
		return e.Date
	default:
		return e.Comment
	}
}

// TagChange is one field's current file value and the value the write sets.
type TagChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// TagEditFile is one file's planned changes, or why it cannot be planned.
type TagEditFile struct {
	Identifier string      `json:"identifier"`
	TrackID    int64       `json:"track_id,omitempty"`
	Title      string      `json:"title,omitempty"`
	Path       string      `json:"path,omitempty"`
	Changes    []TagChange `json:"changes"`
	Error      string      `json:"error,omitempty"`
	ErrorCode  string      `json:"error_code,omitempty"`
	observed   string
}

// TagEditPlan is Engine's review of a reviewed tag edit: each file's before
// and after values read from the file itself. Applying requires its token.
type TagEditPlan struct {
	Files         []TagEditFile `json:"files"`
	ChangedFiles  int           `json:"changed_files"`
	ChangedFields int           `json:"changed_fields"`
	ClearedFields int           `json:"cleared_fields"`
	CanApply      bool          `json:"can_apply"`
	PlanToken     string        `json:"plan_token"`
}

// TagEditResult reports an applied edit per file from the plan it applied.
type TagEditResult struct {
	Status    string        `json:"status"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
	TrackIDs  []int64       `json:"track_ids"`
	Files     []TagEditFile `json:"files"`
}

// Err fails the job only when every file failed, so completed files stay undoable.
func (r *TagEditResult) Err() error {
	if r.Failed > 0 && r.Succeeded == 0 {
		return &progress.Error{Code: "batch_failed", Cause: fmt.Errorf("all %d files failed; inspect per-file results", r.Failed)}
	}
	return nil
}

// TagEditOptions configures reviews and writes.
type TagEditOptions struct {
	Library     string
	AutoInstall bool
	Runner      func(context.Context, string, ...string) ([]byte, error)
}

// ValidateTagEdits checks the request before any file is read.
func ValidateTagEdits(edits []TagEdit) error {
	if len(edits) == 0 {
		return fmt.Errorf("edits must name at least one track")
	}
	if len(edits) > MaxTagEdits {
		return &progress.Error{Code: "batch_limit_exceeded", Cause: fmt.Errorf("edits accepts at most %d files", MaxTagEdits)}
	}
	seen := map[string]bool{}
	for _, edit := range edits {
		if strings.TrimSpace(edit.Identifier) == "" {
			return fmt.Errorf("every edit needs an identifier")
		}
		if seen[edit.Identifier] {
			return fmt.Errorf("track %q is edited twice", edit.Identifier)
		}
		seen[edit.Identifier] = true
		if date := edit.Date; date != nil && *date != "" && !validTagDate(*date) {
			return &progress.Error{Code: "invalid_tag_date", Cause: fmt.Errorf("date %q must be YYYY, YYYY-MM or YYYY-MM-DD", *date)}
		}
	}
	return nil
}

func validTagDate(date string) bool {
	for _, layout := range []string{"2006", "2006-01", "2006-01-02"} {
		if len(date) == len(layout) && validDigits(date, layout) {
			return true
		}
	}
	return false
}

func validDigits(value, layout string) bool {
	for i := range layout {
		if (layout[i] == '-') != (value[i] == '-') || (layout[i] != '-' && (value[i] < '0' || value[i] > '9')) {
			return false
		}
	}
	return true
}

// PreviewTagEdits reads each file's current tags and plans only the fields
// whose value changes. It writes nothing and needs no Desktop check.
func PreviewTagEdits(ctx context.Context, d *sql.DB, edits []TagEdit, opts TagEditOptions) (*TagEditPlan, error) {
	if err := ValidateTagEdits(edits); err != nil {
		return nil, err
	}
	plan := &TagEditPlan{Files: make([]TagEditFile, 0, len(edits))}
	client := acquire.Client{Runner: opts.Runner}
	for i, edit := range edits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := TagEditFile{Identifier: edit.Identifier, Changes: []TagChange{}}
		if err := planTagEdit(ctx, d, client, edit, opts, &file); err != nil {
			// A missing tool fails the whole review: no file can be read.
			if code := progress.ErrorCode(err); code == "tool_unavailable" || code == "tool_outdated" || code == "tool_install_failed" {
				return nil, err
			}
			file.Error, file.ErrorCode, file.Changes = err.Error(), progress.ErrorCode(err), []TagChange{}
		}
		if len(file.Changes) > 0 {
			plan.ChangedFiles++
			plan.ChangedFields += len(file.Changes)
			for _, change := range file.Changes {
				if change.After == "" {
					plan.ClearedFields++
				}
			}
		}
		plan.Files = append(plan.Files, file)
		progress.Item(ctx, "track.tags.edit", i+1, len(edits))
	}
	plan.CanApply = plan.ChangedFiles > 0
	token, err := tagEditToken(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanToken = token
	return plan, nil
}

func planTagEdit(ctx context.Context, d *sql.DB, client acquire.Client, edit TagEdit, opts TagEditOptions, file *TagEditFile) error {
	t, err := db.FindTrack(ctx, d, edit.Identifier)
	if err != nil {
		return err
	}
	if t == nil {
		return &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q not found", edit.Identifier)}
	}
	file.TrackID, file.Title, file.Path = t.ID, t.Title.String, t.PathString()
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
	file.observed = fmt.Sprintf("%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())
	result, err := client.Tags(ctx, "inspect", path, "", acquire.TagOptions{AutoInstall: opts.AutoInstall})
	if err != nil {
		return err
	}
	inspection := result.Results[0].Inspection
	if inspection == nil {
		return fmt.Errorf("tag-track returned no inspection")
	}
	current := map[string]string{"title": inspection.Title, "artist": inspection.Artist, "album": inspection.Album, "genre": inspection.Genre, "date": inspection.Date, "comment": inspection.Comment}
	for _, field := range TagEditFields {
		if value := edit.value(field); value != nil && *value != current[field] {
			file.Changes = append(file.Changes, TagChange{Field: field, Before: current[field], After: *value})
		}
	}
	return nil
}

// tagEditToken binds the plan to its values and to the files as they were
// read, so a file that changes after review makes the write refuse.
func tagEditToken(plan *TagEditPlan) (string, error) {
	type bound struct {
		Identifier string      `json:"identifier"`
		Observed   string      `json:"observed"`
		Changes    []TagChange `json:"changes"`
	}
	files := make([]bound, len(plan.Files))
	for i, file := range plan.Files {
		files[i] = bound{file.Identifier, file.observed, file.Changes}
	}
	data, err := json.Marshal(files)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// CheckTagEdits refuses a stale or empty review before Desktop checks,
// snapshots and history are enrolled.
func CheckTagEdits(ctx context.Context, d *sql.DB, edits []TagEdit, token string, opts TagEditOptions) error {
	plan, err := PreviewTagEdits(ctx, d, edits, opts)
	if err != nil {
		return err
	}
	return checkTagEditPlan(plan, token)
}

func checkTagEditPlan(plan *TagEditPlan, token string) error {
	if token == "" {
		return fmt.Errorf("plan_token is required; review the edit with dry_run first")
	}
	if token != plan.PlanToken {
		return &progress.Error{Code: "file_tags_plan_stale", Cause: errors.New("the files changed since review; review the edit again")}
	}
	if !plan.CanApply {
		return &progress.Error{Code: "file_tags_unchanged", Cause: errors.New("the files already have these values")}
	}
	return nil
}

// ApplyTagEdits plans again, refuses a token that no longer matches, and
// writes each changed file in place with only its changed fields and no
// online lookup. A file that fails is reported and the others still write.
func ApplyTagEdits(ctx context.Context, d *sql.DB, edits []TagEdit, token string, opts TagEditOptions) (*TagEditResult, error) {
	plan, err := PreviewTagEdits(ctx, d, edits, opts)
	if err != nil {
		return nil, err
	}
	if err := checkTagEditPlan(plan, token); err != nil {
		return nil, err
	}
	result := &TagEditResult{Status: "success", TrackIDs: []int64{}, Files: []TagEditFile{}}
	changed := 0
	for _, file := range plan.Files {
		if len(file.Changes) > 0 {
			changed++
		}
	}
	done := 0
	for _, file := range plan.Files {
		if len(file.Changes) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			result.Status = "cancelled"
			return result, err
		}
		options := acquire.TagOptions{Lookup: acquire.LookupNone, AutoInstall: opts.AutoInstall}
		for _, change := range file.Changes {
			value := change.After
			switch change.Field {
			case "title":
				options.Title = &value
			case "artist":
				options.Artist = &value
			case "album":
				options.Album = &value
			case "genre":
				options.Genre = &value
			case "date":
				options.Date = &value
			case "comment":
				options.Comment = &value
			}
		}
		itemCtx := progress.Range(ctx, 10+85*float64(done)/float64(changed), 10+85*float64(done+1)/float64(changed))
		_, err := UpdateFileTags(itemCtx, d, opts.Library, file.Identifier, FileTagOptions{Options: options, Runner: opts.Runner})
		if err != nil {
			file.Error, file.ErrorCode = err.Error(), progress.ErrorCode(err)
			result.Failed++
		} else {
			result.Succeeded++
			result.TrackIDs = append(result.TrackIDs, file.TrackID)
		}
		result.Files = append(result.Files, file)
		done++
		progress.Item(ctx, "track.tags.edit", done, changed)
	}
	if result.Failed > 0 {
		result.Status = "partial"
		if result.Succeeded == 0 {
			result.Status = "failed"
		}
	}
	return result, result.Err()
}
