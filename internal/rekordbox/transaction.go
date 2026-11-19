package rekordbox

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type reviewedWrite struct {
	input   reviewInput
	archive importsource.State
	token   string
}

// writeCurrentReview observes native identity inside the same transaction as
// insertion. The caller owns fresh archive/source/media checks, commit and
// companion publication; no decoding or independent commit happens here.
func writeCurrentReview(ctx context.Context, tx *sql.Tx, request reviewedWrite) (nativeResult, error) {
	in := request.input
	uuid, name, err := destinationIdentity(ctx, tx, importTarget{name: in.options.Name, parentID: in.parentID, unique: in.options.UniqueName})
	if err != nil {
		return nativeResult{}, err
	}
	tracks, err := readNativeTracks(ctx, tx)
	if err != nil {
		return nativeResult{}, err
	}
	in.libraryUUID, in.name, in.native = uuid, name, tracks
	plan, err := acceptReview(in, request.archive, request.token)
	if err != nil {
		return nativeResult{}, err
	}
	return writeReviewed(ctx, tx, plan)
}
