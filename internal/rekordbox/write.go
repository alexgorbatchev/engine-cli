package rekordbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

type preparedTrack struct {
	metadata db.Track
	media    performance
	added    *time.Time
}

func prepareTrack(raw rbxml.Track, song lib.Song, audio audioExtent) (preparedTrack, error) {
	metadata, err := nativeMetadata(raw, song, audio)
	if err != nil {
		return preparedTrack{}, err
	}
	media, err := preparePerformance(raw, song, audio.rate, audio.frames)
	if err != nil {
		return preparedTrack{}, err
	}
	prepared := preparedTrack{metadata: metadata, media: media}
	if raw.DateAdded != "" {
		added := time.Unix(int64(song.DateAdded), 0).UTC()
		prepared.added = &added
	}
	return prepared, nil
}

// insertPrepared writes only already validated values. The caller owns the
// transaction, source-identity checks and commit; decoding never runs here.
func insertPrepared(ctx context.Context, tx *sql.Tx, prepared preparedTrack) (int64, error) {
	id, err := db.InsertTrack(ctx, tx, &prepared.metadata)
	if err != nil {
		return 0, err
	}
	if prepared.added != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE Track SET dateAdded=? WHERE id=?", *prepared.added, id); err != nil {
			return 0, fmt.Errorf("preserving source date added: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, "UPDATE PerformanceData SET beatData=?,quickCues=?,loops=? WHERE trackId=?", prepared.media.beat, prepared.media.cues, prepared.media.loops, id)
	if err != nil {
		return 0, fmt.Errorf("writing imported performance: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking imported performance write: %w", err)
	}
	if count != 1 {
		return 0, fmt.Errorf("native performance row was not updated")
	}
	return id, nil
}
