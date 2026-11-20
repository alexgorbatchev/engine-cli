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
	"slices"
	"strconv"
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
// written to several files. Take names fields whose file value is copied into
// the track's own info instead, such as the file's artist into the track's
// artist; a field is either set in the file or taken from it, never both.
type TagEdit struct {
	Identifier string   `json:"identifier"`
	Title      *string  `json:"title,omitempty"`
	Artist     *string  `json:"artist,omitempty"`
	Album      *string  `json:"album,omitempty"`
	Genre      *string  `json:"genre,omitempty"`
	Date       *string  `json:"date,omitempty"`
	Comment    *string  `json:"comment,omitempty"`
	Take       []string `json:"take,omitempty"`
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
	// TrackChanges are the track info values taken from the file, track
	// before and file value after, named by track field.
	TrackChanges []TagChange `json:"track_changes"`
	Error        string      `json:"error,omitempty"`
	ErrorCode    string      `json:"error_code,omitempty"`
	observed     string
}

// TagEditPlan is Engine's review of a reviewed tag edit: each file's before
// and after values read from the file itself. Applying requires its token.
type TagEditPlan struct {
	Files         []TagEditFile `json:"files"`
	ChangedFiles  int           `json:"changed_files"`
	ChangedFields int           `json:"changed_fields"`
	ClearedFields int           `json:"cleared_fields"`
	ChangedTracks int           `json:"changed_tracks"`
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
	// UpdatedTracks counts the tracks whose own info took file values.
	UpdatedTracks int `json:"updated_tracks"`
}

// Err fails the job only when every file failed, so completed files stay undoable.
func (r *TagEditResult) Err() error {
	if r.Failed > 0 && r.Succeeded == 0 && r.UpdatedTracks == 0 {
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
		for i, field := range edit.Take {
			if !slices.Contains(TagEditFields, field) {
				return fmt.Errorf("take names unknown field %q", field)
			}
			if slices.Contains(edit.Take[:i], field) {
				return fmt.Errorf("take names %q twice", field)
			}
			if edit.value(field) != nil {
				return fmt.Errorf("track %q both sets and takes %s; choose one", edit.Identifier, field)
			}
		}
	}
	return nil
}

// trackField is the track info column a file field is taken into: the file's
// date becomes the track's year.
func trackField(field string) string {
	if field == "date" {
		return "year"
	}
	return field
}

// trackValue reads a track's own value for a file field, as review text.
func trackValue(t *db.Track, field string) string {
	switch field {
	case "title":
		return t.Title.String
	case "artist":
		return t.Artist.String
	case "album":
		return t.Album.String
	case "genre":
		return t.Genre.String
	case "comment":
		return t.Comment.String
	default:
		if !t.Year.Valid || t.Year.Int64 == 0 {
			return ""
		}
		return strconv.FormatInt(t.Year.Int64, 10)
	}
}

// takenValue is the track info value a file value becomes: the year of a
// YYYY, YYYY-MM or YYYY-MM-DD date, and every other field as it is.
func takenValue(field, value string) (string, error) {
	if field != "date" || value == "" {
		return value, nil
	}
	if !validTagDate(value) {
		return "", &progress.Error{Code: "invalid_tag_date", Cause: fmt.Errorf("the file's date %q is not YYYY, YYYY-MM or YYYY-MM-DD", value)}
	}
	return value[:4], nil
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
		file := TagEditFile{Identifier: edit.Identifier, Changes: []TagChange{}, TrackChanges: []TagChange{}}
		if err := planTagEdit(ctx, d, client, edit, opts, &file); err != nil {
			// A missing tool fails the whole review: no file can be read.
			if code := progress.ErrorCode(err); code == "tool_unavailable" || code == "tool_outdated" || code == "tool_install_failed" {
				return nil, err
			}
			file.Error, file.ErrorCode, file.Changes, file.TrackChanges = err.Error(), progress.ErrorCode(err), []TagChange{}, []TagChange{}
		}
		if len(file.TrackChanges) > 0 {
			plan.ChangedTracks++
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
	plan.CanApply = plan.ChangedFiles > 0 || plan.ChangedTracks > 0
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
	for _, field := range TagEditFields {
		if !slices.Contains(edit.Take, field) {
			continue
		}
		after, err := takenValue(field, current[field])
		if err != nil {
			return err
		}
		if before := trackValue(t, field); before != after {
			file.TrackChanges = append(file.TrackChanges, TagChange{Field: trackField(field), Before: before, After: after})
		}
	}
	return nil
}

// tagEditToken binds the plan to its values and to the files as they were
// read, so a file that changes after review makes the write refuse.
func tagEditToken(plan *TagEditPlan) (string, error) {
	type bound struct {
		Identifier   string      `json:"identifier"`
		Observed     string      `json:"observed"`
		Changes      []TagChange `json:"changes"`
		TrackChanges []TagChange `json:"track_changes"`
	}
	files := make([]bound, len(plan.Files))
	for i, file := range plan.Files {
		files[i] = bound{file.Identifier, file.observed, file.Changes, file.TrackChanges}
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
	if err := takeTrackValues(ctx, d, plan, result); err != nil {
		return result, err
	}
	if result.Failed > 0 {
		result.Status = "partial"
		if result.Succeeded == 0 && result.UpdatedTracks == 0 {
			result.Status = "failed"
		}
	}
	return result, result.Err()
}

// takeTrackValues writes the reviewed track info values taken from files, in
// one transaction after the file writes, skipping files whose write failed.
func takeTrackValues(ctx context.Context, d *sql.DB, plan *TagEditPlan, result *TagEditResult) error {
	failed := map[string]bool{}
	for _, file := range result.Files {
		if file.ErrorCode != "" || file.Error != "" {
			failed[file.Identifier] = true
		}
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, file := range plan.Files {
		if len(file.TrackChanges) == 0 || failed[file.Identifier] {
			continue
		}
		update := metadataUpdate{ID: file.TrackID}
		for _, change := range file.TrackChanges {
			update.Columns = append(update.Columns, change.Field+" = ?")
			if change.Field == "year" {
				year := int64(0)
				if change.After != "" {
					year, _ = strconv.ParseInt(change.After, 10, 64)
				}
				update.Values = append(update.Values, year)
			} else {
				update.Values = append(update.Values, change.After)
			}
		}
		if err := saveMetadata(ctx, tx, update); err != nil {
			return err
		}
		result.UpdatedTracks++
		if !slices.Contains(result.TrackIDs, file.TrackID) {
			result.TrackIDs = append(result.TrackIDs, file.TrackID)
		}
	}
	return tx.Commit()
}
