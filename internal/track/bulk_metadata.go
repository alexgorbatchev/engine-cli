package track

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const MaxMetadataBatch = 500

// BatchMetadataEdit applies the supplied values to every selected track atomically.
type BatchMetadataEdit struct {
	Identifiers []string `json:"identifiers"`
	MetadataFields
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
	columns, _ := e.fields()
	if len(columns) == 0 {
		return fmt.Errorf("choose at least one field to update")
	}
	return nil
}

// EditMetadataBatch resolves the complete selection before writing and deduplicates
// aliases in first-seen order. A failed write rolls the whole batch back.
func EditMetadataBatch(ctx context.Context, database *sql.DB, edit BatchMetadataEdit) ([]int64, error) {
	if err := edit.Validate(); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(edit.Identifiers))
	seen := make(map[int64]bool)
	for _, identifier := range edit.Identifiers {
		t, err := db.FindTrack(ctx, database, identifier)
		if err != nil {
			return nil, fmt.Errorf("finding track to edit: %w", err)
		}
		if t == nil {
			return nil, &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q: %w", identifier, sql.ErrNoRows)}
		}
		if !seen[t.ID] {
			ids = append(ids, t.ID)
			seen[t.ID] = true
		}
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		columns, values := edit.fields()
		if err := saveMetadata(ctx, tx, metadataUpdate{ID: id, Columns: columns, Values: values}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
