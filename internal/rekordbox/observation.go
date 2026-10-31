package rekordbox

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type destinationRequest struct {
	library, name string
	source        source
}

type destinationObservation struct {
	uuid    string
	tracks  []nativeTrack
	archive importsource.State
	review  archiveReview
}

// The scheduler's collection read barrier must cover this complete observation.
// The read transaction pins Engine rows; the same barrier protects the separate
// companion publication. Neither an absent companion nor a dry run enrolls it.
func observeDestination(ctx context.Context, d *sql.DB, request destinationRequest) (destinationObservation, error) {
	archive, err := importsource.Read(ctx, request.library)
	if err != nil {
		return destinationObservation{}, err
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return destinationObservation{}, err
	}
	defer tx.Rollback()
	uuid, err := destinationIdentity(ctx, tx, request.name)
	if err != nil {
		return destinationObservation{}, err
	}
	tracks, err := readNativeTracks(ctx, tx)
	if err != nil {
		return destinationObservation{}, err
	}
	return destinationObservation{uuid: uuid, tracks: tracks, archive: archive, review: reviewArchiveTracks(request.source, archive, tracks)}, nil
}
