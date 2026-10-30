package track

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// FindReplaceOptions transforms selected collection text without changing audio tags.
// Regex replacements use Go's $name/${name} capture expansion; literal mode keeps dollars literal.
type FindReplaceOptions struct {
	Identifiers []string `json:"identifiers"`
	Fields      []string `json:"fields"`
	Find        string   `json:"find"`
	Replacement *string  `json:"replacement"`
	Mode        string   `json:"mode"`
	MatchCase   bool     `json:"match_case"`
	DryRun      bool     `json:"dry_run"`
	PlanToken   string   `json:"plan_token"`
}

var transformFields = []string{"title", "artist", "album", "genre", "comment", "label", "composer", "remixer"}

func (o FindReplaceOptions) Validate() error {
	if len(o.Identifiers) == 0 || len(o.Identifiers) > MaxMetadataBatch {
		return fmt.Errorf("choose between 1 and %d tracks", MaxMetadataBatch)
	}
	for _, id := range o.Identifiers {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("choose a track for every identifier")
		}
	}
	if len(o.Fields) == 0 || len(o.Fields) > len(transformFields) {
		return fmt.Errorf("choose text fields to replace")
	}
	seen := make(map[string]bool)
	for _, field := range o.Fields {
		if !slices.Contains(transformFields, field) || seen[field] {
			return fmt.Errorf("choose each supported text field at most once")
		}
		seen[field] = true
	}
	if o.Replacement == nil || !utf8.ValidString(*o.Replacement) || len(*o.Replacement) > 65536 {
		return fmt.Errorf("supply replacement text, including an empty string to remove matches")
	}
	_, err := o.matcher()
	return err
}

func (o FindReplaceOptions) matcher() (*regexp.Regexp, error) {
	if o.Find == "" || !utf8.ValidString(o.Find) || len(o.Find) > 4096 {
		return nil, fmt.Errorf("supply nonempty find text of at most 4096 bytes")
	}
	pattern := o.Find
	switch o.Mode {
	case "", "literal":
		pattern = regexp.QuoteMeta(pattern)
	case "regex":
	default:
		return nil, fmt.Errorf("choose literal or regex matching")
	}
	if !o.MatchCase {
		pattern = "(?i:" + pattern + ")"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("supply a valid Go regular expression")
	}
	return re, nil
}

// MetadataReplacement describes the exact proposed field change, including invalid titles.
type MetadataReplacement struct {
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
	ErrorCode string `json:"error_code,omitempty"`
}

type TrackReplacements struct {
	TrackID int64                 `json:"track_id"`
	Title   string                `json:"title"`
	Changes []MetadataReplacement `json:"changes"`
}

type FindReplacePlan struct {
	Tracks         []TrackReplacements `json:"tracks"`
	SelectedTracks int                 `json:"selected_tracks"`
	ChangedTracks  int                 `json:"changed_tracks"`
	ChangedFields  int                 `json:"changed_fields"`
	CanApply       bool                `json:"can_apply"`
	PlanToken      string              `json:"plan_token"`
}

type FindReplaceResult struct {
	TrackIDs      []int64 `json:"track_ids"`
	ChangedFields int     `json:"changed_fields"`
}

// PreviewFindReplace never writes or enrolls a safety snapshot.
func PreviewFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*FindReplacePlan, error) {
	ids, root, err := resolveTransform(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return findReplacePlan(ctx, tx, root, ids, opts)
}

// CheckFindReplace runs before Desktop checks, snapshots and history enrollment.
func CheckFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*FindReplacePlan, error) {
	plan, err := PreviewFindReplace(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	return plan, checkTransformPlan(plan, opts.PlanToken)
}

func checkTransformPlan(plan *FindReplacePlan, token string) error {
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

func resolveTransform(ctx context.Context, d *sql.DB, opts FindReplaceOptions) ([]int64, string, error) {
	if d == nil {
		return nil, "", fmt.Errorf("database cannot be nil")
	}
	if err := opts.Validate(); err != nil {
		return nil, "", err
	}
	ids := make([]int64, 0, len(opts.Identifiers))
	selectorBytes := 0
	for _, selector := range opts.Identifiers {
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

func findReplacePlan(ctx context.Context, q db.Executor, root string, ids []int64, opts FindReplaceOptions) (*FindReplacePlan, error) {
	re, err := opts.matcher()
	if err != nil {
		return nil, err
	}
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
	plan := &FindReplacePlan{Tracks: make([]TrackReplacements, 0, len(ids)), SelectedTracks: len(ids)}
	evidence := make([]transformEvidence, 0, len(ids))
	valid := true
	evidenceBytes, outputBytes := int(uuidBytes), 0
	replacement := newBoundedReplacement(re, *opts.Replacement, opts.Mode == "regex")
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
		tr := TrackReplacements{TrackID: id, Title: e.Values[0].String, Changes: []MetadataReplacement{}}
		for _, field := range opts.Fields {
			before := e.Values[slices.Index(transformFields, field)]
			if !before.Valid {
				continue
			}
			after, err := replacement.replace(ctx, before.String, transformBatchBytes-outputBytes)
			if err != nil {
				return nil, err
			}
			outputBytes += len(after)
			if after == before.String {
				continue
			}
			change := MetadataReplacement{Field: field, Before: before.String, After: after}
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
	if opts.Mode == "" {
		opts.Mode = "literal"
	}
	opts.DryRun = false
	opts.PlanToken = ""
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(struct {
		UUID, Root string
		Options    FindReplaceOptions
		Tracks     []transformEvidence
	}{uuid, root, opts, evidence})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes)
	plan.PlanToken = hex.EncodeToString(sum[:])
	return plan, nil
}

// ApplyFindReplace rechecks the exact review inside the single metadata transaction.
func ApplyFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*FindReplaceResult, error) {
	ids, root, err := resolveTransform(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := findReplacePlan(ctx, tx, root, ids, opts)
	if err != nil {
		return nil, err
	}
	if err := checkTransformPlan(plan, opts.PlanToken); err != nil {
		return nil, err
	}
	result := &FindReplaceResult{TrackIDs: []int64{}, ChangedFields: plan.ChangedFields}
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
