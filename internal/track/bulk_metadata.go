package track

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/musicalkey"
)

const MaxMetadataBatch = 500

// BatchMetadataEdit applies the supplied values to every selected track atomically. DryRun reviews the per-track
// changes without writing; PlanToken binds an apply to that exact review, and an apply without one writes at once.
type BatchMetadataEdit struct {
	Identifiers []string `json:"identifiers"`
	MetadataFields
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token"`
}

// FieldChange is one field's value before and after an edit. Values are JSON scalars; null means the field is empty
// (no text, year 0, unrated, or no key), so clearing an empty field is no change.
type FieldChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// TrackEdit lists one selected track's changed fields; unchanged tracks have none. Title is the display title read
// before the edit: the track's title, or its filename without an extension.
type TrackEdit struct {
	TrackID int64         `json:"track_id"`
	Title   string        `json:"title"`
	Changes []FieldChange `json:"changes"`
}

// UnresolvedTrack is a selector that names no track in the library.
type UnresolvedTrack struct {
	Identifier string `json:"identifier"`
	ErrorCode  string `json:"error_code"`
}

// BatchEditPlan reviews a batch edit. It can apply only when every selector resolves and some field changes.
type BatchEditPlan struct {
	Tracks         []TrackEdit       `json:"tracks"`
	Unresolved     []UnresolvedTrack `json:"unresolved"`
	SelectedTracks int               `json:"selected_tracks"`
	ChangedTracks  int               `json:"changed_tracks"`
	ChangedFields  int               `json:"changed_fields"`
	CanApply       bool              `json:"can_apply"`
	PlanToken      string            `json:"plan_token"`
}

// BatchEditResult reports the committed edit from the plan it applied: the tracks it changed and, per track, each
// field's value before and after.
type BatchEditResult struct {
	TrackIDs      []int64     `json:"track_ids"`
	ChangedFields int         `json:"changed_fields"`
	Tracks        []TrackEdit `json:"tracks"`
}

func (e BatchMetadataEdit) Validate() error {
	if len(e.Identifiers) == 0 || len(e.Identifiers) > MaxMetadataBatch {
		return fmt.Errorf("choose between 1 and %d tracks", MaxMetadataBatch)
	}
	for _, identifier := range e.Identifiers {
		if strings.TrimSpace(identifier) == "" {
			return fmt.Errorf("choose a track for every identifier")
		}
	}
	if err := e.MetadataFields.validate(); err != nil {
		return err
	}
	if len(e.edits()) == 0 {
		return fmt.Errorf("choose at least one field to update")
	}
	if e.DryRun && e.PlanToken != "" {
		return fmt.Errorf("review without a plan token, then apply with it")
	}
	return nil
}

// fieldEdit is one requested value: After is the reviewed value (nil when it empties the field), Store the column
// value written.
type fieldEdit struct {
	Field, Column string
	After, Store  any
}

// emptyText reports empty optional text as nil, so null and "" compare equal.
func emptyText(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

// edits lists the supplied fields in request order.
func (e MetadataFields) edits() []fieldEdit {
	var edits []fieldEdit
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"title", e.Title}, {"artist", e.Artist}, {"album", e.Album}, {"genre", e.Genre},
		{"comment", e.Comment}, {"label", e.Label}, {"composer", e.Composer}, {"remixer", e.Remixer},
	} {
		if field.value != nil {
			edits = append(edits, fieldEdit{Field: field.name, Column: field.name, After: emptyText(sql.NullString{String: *field.value, Valid: true}), Store: *field.value})
		}
	}
	if e.Year != nil {
		edits = append(edits, fieldEdit{Field: "year", Column: "year", After: emptyNumber(*e.Year, 0), Store: *e.Year})
	}
	if e.RatingStars != nil {
		edits = append(edits, fieldEdit{Field: "rating_stars", Column: "rating", After: emptyNumber(*e.RatingStars, 0), Store: *e.RatingStars * RatingUnitsPerStar})
	}
	// Only the collection column changes; the analysis key saved in PerformanceData.trackData is preserved.
	if e.Key != nil {
		edit := fieldEdit{Field: "key", Column: "key", After: emptyNumber(*e.Key, musicalkey.None), Store: *e.Key}
		if *e.Key == musicalkey.None {
			edit.Store = nil
		}
		edits = append(edits, edit)
	}
	return edits
}

func emptyNumber(value, empty int64) any {
	if value == empty {
		return nil
	}
	return value
}

// batchEvidence is what a review binds: the track's identity, location, edit time and the edited fields' values.
type batchEvidence struct {
	ID       int64
	OriginID sql.NullInt64
	Origin   sql.NullString
	Path     sql.NullString
	LastEdit sql.NullString
	Before   []any
}

// readBatchTrack reads the evidence and the reviewed value of every edited field.
func readBatchTrack(ctx context.Context, q db.Executor, id int64, edits []fieldEdit) (batchEvidence, string, error) {
	var e batchEvidence
	var title, filename sql.NullString
	var year, rating, key sql.NullInt64
	text := make([]sql.NullString, len(transformFields))
	args := []any{&e.OriginID, &e.Origin, &e.Path, &e.LastEdit, &filename, &year, &rating, &key}
	for i := range text {
		args = append(args, &text[i])
	}
	query := "SELECT originTrackId,originDatabaseUuid,path,lastEditTime,filename,year,rating,key," + strings.Join(transformFields, ",") + " FROM Track WHERE id=?"
	if err := q.QueryRowContext(ctx, query, id).Scan(args...); err != nil {
		return e, "", err
	}
	e.ID = id
	title = text[0]
	for _, edit := range edits {
		var before any
		switch edit.Field {
		case "year":
			before = emptyNumber(year.Int64, 0)
		case "rating_stars":
			before = emptyNumber(min(MaxRatingStars, max(0, rating.Int64/RatingUnitsPerStar)), 0)
		case "key":
			before = nil
			if key.Valid {
				before = emptyNumber(key.Int64, musicalkey.None)
			}
		default:
			for i, name := range transformFields {
				if name == edit.Field {
					before = emptyText(text[i])
				}
			}
		}
		e.Before = append(e.Before, before)
	}
	display := title.String
	if strings.TrimSpace(display) == "" {
		display = strings.TrimSuffix(filename.String, filepath.Ext(filename.String))
	}
	return e, display, nil
}

// resolveBatch resolves selectors in first-seen order, deduplicating aliases; selectors naming no track are returned
// separately with not_found.
func resolveBatch(ctx context.Context, d *sql.DB, identifiers []string) ([]int64, []UnresolvedTrack, error) {
	ids := []int64{}
	unresolved := []UnresolvedTrack{}
	seen := map[int64]bool{}
	for _, identifier := range identifiers {
		t, err := db.FindTrack(ctx, d, identifier)
		if errors.Is(err, sql.ErrNoRows) {
			t, err = nil, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("finding track to edit: %w", err)
		}
		if t == nil {
			unresolved = append(unresolved, UnresolvedTrack{Identifier: identifier, ErrorCode: "not_found"})
			continue
		}
		if !seen[t.ID] {
			ids = append(ids, t.ID)
			seen[t.ID] = true
		}
	}
	return ids, unresolved, nil
}

func (e BatchMetadataEdit) plan(ctx context.Context, q db.Executor, root string, ids []int64, unresolved []UnresolvedTrack) (*BatchEditPlan, error) {
	edits := e.edits()
	var uuid string
	if err := q.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return nil, err
	}
	plan := &BatchEditPlan{Tracks: make([]TrackEdit, 0, len(ids)), Unresolved: unresolved, SelectedTracks: len(ids)}
	evidence := make([]batchEvidence, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ev, title, err := readBatchTrack(ctx, q, id, edits)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, ev)
		tr := TrackEdit{TrackID: id, Title: title, Changes: []FieldChange{}}
		for i, edit := range edits {
			if ev.Before[i] == edit.After {
				continue
			}
			tr.Changes = append(tr.Changes, FieldChange{Field: edit.Field, Before: ev.Before[i], After: edit.After})
		}
		if len(tr.Changes) > 0 {
			plan.ChangedTracks++
			plan.ChangedFields += len(tr.Changes)
		}
		plan.Tracks = append(plan.Tracks, tr)
	}
	plan.CanApply = len(unresolved) == 0 && plan.ChangedFields > 0
	stored := make([][2]any, len(edits))
	for i, edit := range edits {
		stored[i] = [2]any{edit.Column, edit.Store}
	}
	encoded, err := json.Marshal(struct {
		UUID, Root string
		Edits      [][2]any
		Unresolved []UnresolvedTrack
		Tracks     []batchEvidence
	}{uuid, root, stored, unresolved, evidence})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	plan.PlanToken = hex.EncodeToString(sum[:])
	return plan, nil
}

func batchEditError(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}

// PreviewMetadataBatch reviews the edit in a read-only transaction; it never writes or enrolls a safety snapshot.
func PreviewMetadataBatch(ctx context.Context, d *sql.DB, edit BatchMetadataEdit) (*BatchEditPlan, error) {
	if err := edit.Validate(); err != nil {
		return nil, err
	}
	ids, unresolved, err := resolveBatch(ctx, d, edit.Identifiers)
	if err != nil {
		return nil, err
	}
	root, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return edit.plan(ctx, tx, root, ids, unresolved)
}

// checkBatchPlan refuses a missing selection, a stale review and a reviewed plan with nothing to change.
func checkBatchPlan(plan *BatchEditPlan, token string) error {
	if len(plan.Unresolved) > 0 {
		return &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q: %w", plan.Unresolved[0].Identifier, sql.ErrNoRows)}
	}
	if token == "" {
		return nil
	}
	if token != plan.PlanToken {
		return batchEditError("metadata_edit_stale", "review the current track information before applying the edit")
	}
	if !plan.CanApply {
		return batchEditError("metadata_edit_unchanged", "the reviewed edit changes nothing")
	}
	return nil
}

// CheckMetadataBatch runs before Desktop checks, snapshots and history enrollment, so a refused apply leaves none.
func CheckMetadataBatch(ctx context.Context, d *sql.DB, edit BatchMetadataEdit) error {
	review := edit
	review.DryRun, review.PlanToken = true, ""
	plan, err := PreviewMetadataBatch(ctx, d, review)
	if err != nil {
		return err
	}
	return checkBatchPlan(plan, edit.PlanToken)
}

// EditMetadataBatch resolves the complete selection before writing and deduplicates aliases in first-seen order. It
// plans again inside the write transaction, rechecks a supplied plan token there, and writes only changed fields; a
// failed write rolls the whole batch back.
func EditMetadataBatch(ctx context.Context, d *sql.DB, edit BatchMetadataEdit) (*BatchEditResult, error) {
	if err := edit.Validate(); err != nil {
		return nil, err
	}
	if edit.DryRun {
		return nil, fmt.Errorf("a dry run reviews the edit; preview it instead")
	}
	ids, unresolved, err := resolveBatch(ctx, d, edit.Identifiers)
	if err != nil {
		return nil, err
	}
	root, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	review := edit
	review.PlanToken = ""
	plan, err := review.plan(ctx, tx, root, ids, unresolved)
	if err != nil {
		return nil, err
	}
	if err := checkBatchPlan(plan, edit.PlanToken); err != nil {
		return nil, err
	}
	edits := edit.edits()
	result := &BatchEditResult{TrackIDs: []int64{}, ChangedFields: plan.ChangedFields, Tracks: []TrackEdit{}}
	for _, tr := range plan.Tracks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(tr.Changes) == 0 {
			continue
		}
		update := metadataUpdate{ID: tr.TrackID}
		for _, change := range tr.Changes {
			for _, e := range edits {
				if e.Field != change.Field {
					continue
				}
				if e.Store == nil {
					update.Columns = append(update.Columns, e.Column+" = NULL")
					continue
				}
				update.Columns = append(update.Columns, e.Column+" = ?")
				update.Values = append(update.Values, e.Store)
			}
		}
		if err := saveMetadata(ctx, tx, update); err != nil {
			return nil, err
		}
		result.TrackIDs = append(result.TrackIDs, tr.TrackID)
		result.Tracks = append(result.Tracks, tr)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
