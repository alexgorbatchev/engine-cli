package track

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

var transformFields = []string{"title", "artist", "album", "genre", "comment", "label", "composer", "remixer"}

func validateTransformSelection(identifiers, fields []string) error {
	if len(identifiers) == 0 || len(identifiers) > MaxMetadataBatch {
		return fmt.Errorf("choose between 1 and %d tracks", MaxMetadataBatch)
	}
	for _, id := range identifiers {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("choose a track for every identifier")
		}
	}
	if len(fields) == 0 || len(fields) > len(transformFields) {
		return fmt.Errorf("choose text fields to transform")
	}
	seen := make(map[string]bool)
	for _, field := range fields {
		if !slices.Contains(transformFields, field) || seen[field] {
			return fmt.Errorf("choose each supported text field at most once")
		}
		seen[field] = true
	}
	return nil
}

// MetadataChange describes the exact proposed field change, including invalid titles.
type MetadataChange struct {
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
	ErrorCode string `json:"error_code,omitempty"`
}

type TrackTransform struct {
	TrackID int64            `json:"track_id"`
	Title   string           `json:"title"`
	Changes []MetadataChange `json:"changes"`
}

type MetadataTransformPlan struct {
	Tracks         []TrackTransform `json:"tracks"`
	SelectedTracks int              `json:"selected_tracks"`
	ChangedTracks  int              `json:"changed_tracks"`
	ChangedFields  int              `json:"changed_fields"`
	CanApply       bool             `json:"can_apply"`
	PlanToken      string           `json:"plan_token"`
}

type MetadataTransformResult struct {
	TrackIDs      []int64 `json:"track_ids"`
	ChangedFields int     `json:"changed_fields"`
}

func checkTransformPlan(plan *MetadataTransformPlan, token string) error {
	if token == "" || token != plan.PlanToken {
		return transformError("metadata_transform_stale", "review current metadata before applying replacements")
	}
	if !plan.CanApply {
		return transformError("metadata_transform_unavailable", "review matching changes and resolve invalid titles")
	}
	return nil
}

func transformError(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}

func resolveTransformIdentifiers(ctx context.Context, d *sql.DB, identifiers []string) ([]int64, string, error) {
	ids := make([]int64, 0, len(identifiers))
	selectorBytes := 0
	for _, selector := range identifiers {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if len(selector) > transformFieldBytes || len(selector) > transformBatchBytes-selectorBytes {
			return nil, "", transformTooLarge()
		}
		selectorBytes += len(selector)
		selector = strings.TrimSpace(selector)
		var id int64
		var err error
		switch {
		case strings.HasPrefix(selector, "eng:"):
			id, err = identity.ResolveDatabase(ctx, d, "track", selector)
		default:
			id, err = strconv.ParseInt(selector, 10, 64)
			if err != nil || id <= 0 {
				err = d.QueryRowContext(ctx, "SELECT id FROM Track WHERE path=?", selector).Scan(&id)
			} else {
				err = d.QueryRowContext(ctx, "SELECT id FROM Track WHERE id=?", id).Scan(&id)
			}
		}
		if err == sql.ErrNoRows {
			return nil, "", transformError("not_found", "a selected track no longer exists")
		}
		if err != nil {
			return nil, "", err
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	root, err := identity.LibraryPath(ctx, d)
	return ids, root, err
}

type transformEvidence struct {
	ID                                   int64
	OriginID                             sql.NullInt64
	OriginUUID, Path, Filename, LastEdit sql.NullString
	Values                               []sql.NullString
}

type metadataTransform struct {
	Identifiers []string
	Fields      []string
	Options     any
	Replace     func(context.Context, string, int) (string, error)
}

func (t metadataTransform) plan(ctx context.Context, q db.Executor, root string, ids []int64) (*MetadataTransformPlan, error) {
	var uuidBytes int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(length(CAST(uuid AS BLOB)),0) FROM Information WHERE id=1").Scan(&uuidBytes); err != nil {
		return nil, err
	}
	if uuidBytes > transformFieldBytes {
		return nil, transformTooLarge()
	}
	var uuid string
	if err := q.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return nil, err
	}
	plan := &MetadataTransformPlan{Tracks: make([]TrackTransform, 0, len(ids)), SelectedTracks: len(ids)}
	evidence := make([]transformEvidence, 0, len(ids))
	valid := true
	evidenceBytes, outputBytes := int(uuidBytes), 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Check byte lengths within this transaction before hydrating text. Selector
		// resolution likewise selects only IDs, never the full unbounded Track row.
		columns := []string{"originDatabaseUuid", "path", "filename", "lastEditTime"}
		columns = append(columns, transformFields...)
		lengths := make([]string, len(columns))
		for i, column := range columns {
			lengths[i] = "COALESCE(length(CAST(" + column + " AS BLOB)),0)"
		}
		var largest, total int64
		if err := q.QueryRowContext(ctx, "SELECT max("+strings.Join(lengths, ",")+"),"+strings.Join(lengths, "+")+" FROM Track WHERE id=?", id).Scan(&largest, &total); err != nil {
			return nil, err
		}
		if largest > transformFieldBytes || total > int64(transformBatchBytes-evidenceBytes) {
			return nil, transformTooLarge()
		}
		evidenceBytes += int(total)
		e := transformEvidence{ID: id, Values: make([]sql.NullString, len(transformFields))}
		args := []any{&e.OriginID, &e.OriginUUID, &e.Path, &e.Filename, &e.LastEdit}
		for i := range e.Values {
			args = append(args, &e.Values[i])
		}
		if err := q.QueryRowContext(ctx, "SELECT originTrackId,originDatabaseUuid,path,filename,lastEditTime,title,artist,album,genre,comment,label,composer,remixer FROM Track WHERE id=?", id).Scan(args...); err != nil {
			return nil, err
		}
		evidence = append(evidence, e)
		tr := TrackTransform{TrackID: id, Title: e.Values[0].String, Changes: []MetadataChange{}}
		for _, field := range t.Fields {
			before := e.Values[slices.Index(transformFields, field)]
			if !before.Valid {
				continue
			}
			after, err := t.Replace(ctx, before.String, transformBatchBytes-outputBytes)
			if err != nil {
				return nil, err
			}
			outputBytes += len(after)
			if after == before.String {
				continue
			}
			change := MetadataChange{Field: field, Before: before.String, After: after}
			if field == "title" && strings.TrimSpace(after) == "" {
				change.ErrorCode = "metadata_title_required"
				valid = false
			}
			tr.Changes = append(tr.Changes, change)
			plan.ChangedFields++
		}
		if len(tr.Changes) > 0 {
			plan.ChangedTracks++
		}
		plan.Tracks = append(plan.Tracks, tr)
	}
	plan.CanApply = valid && plan.ChangedFields > 0
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(struct {
		UUID, Root string
		Options    any
		Tracks     []transformEvidence
	}{uuid, root, t.Options, evidence})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes)
	plan.PlanToken = hex.EncodeToString(sum[:])
	return plan, nil
}

func applyMetadataTransform(ctx context.Context, tx *sql.Tx, plan *MetadataTransformPlan) (*MetadataTransformResult, error) {
	result := &MetadataTransformResult{TrackIDs: []int64{}, ChangedFields: plan.ChangedFields}
	for _, tr := range plan.Tracks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(tr.Changes) == 0 {
			continue
		}
		update := metadataUpdate{ID: tr.TrackID}
		for _, change := range tr.Changes {
			update.Columns = append(update.Columns, change.Field+" = ?")
			update.Values = append(update.Values, change.After)
		}
		if err := saveMetadata(ctx, tx, update); err != nil {
			return nil, err
		}
		result.TrackIDs = append(result.TrackIDs, tr.TrackID)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (t metadataTransform) preview(ctx context.Context, d *sql.DB) (*MetadataTransformPlan, error) {
	if d == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	ids, root, err := resolveTransformIdentifiers(ctx, d, t.Identifiers)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return t.plan(ctx, tx, root, ids)
}

func (t metadataTransform) apply(ctx context.Context, d *sql.DB, token string) (*MetadataTransformResult, error) {
	if d == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	ids, root, err := resolveTransformIdentifiers(ctx, d, t.Identifiers)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := t.plan(ctx, tx, root, ids)
	if err != nil {
		return nil, err
	}
	if err := checkTransformPlan(plan, token); err != nil {
		return nil, err
	}
	return applyMetadataTransform(ctx, tx, plan)
}
