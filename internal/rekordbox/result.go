package rekordbox

import (
	"context"
	"database/sql"
)

// Result is emitted only after both the Engine transaction and preserved source
// publication succeed. ReusedCount includes retained and explicitly reused tracks.
type Result struct {
	RootID       int64  `json:"root_id"`
	DocumentID   string `json:"document_id"`
	CreatedCount int    `json:"created_count"`
	ReusedCount  int    `json:"reused_count"`
	SkippedCount int    `json:"skipped_count"`
}

type importCompletion struct {
	publication archivePublication
	documentID  string
	native      nativeResult
}

func finishImport(ctx context.Context, tx *sql.Tx, completion importCompletion) (Result, error) {
	if err := commitAndPublish(ctx, tx, completion.publication); err != nil {
		return Result{}, err
	}
	return Result{
		RootID:       completion.native.rootID,
		DocumentID:   completion.documentID,
		CreatedCount: completion.native.created,
		ReusedCount:  completion.native.reused,
		SkippedCount: completion.native.skipped,
	}, nil
}
