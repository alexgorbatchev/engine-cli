package rekordbox

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

type tokenTrack struct {
	ID               int64
	Path, OriginUUID string
	OriginID         int64
}

type tokenPrepared struct {
	SourceID          string
	Metadata          db.Track
	Beat, Cues, Loops []byte
	Added             *time.Time
}

func reviewFingerprint(in reviewInput, review Review) (string, error) {
	tracks := make([]tokenTrack, len(in.native))
	for i, track := range in.native {
		tracks[i] = tokenTrack{ID: track.id, Path: track.path, OriginUUID: track.origin.uuid, OriginID: track.origin.id}
	}
	prepared := []tokenPrepared{}
	for _, entry := range in.entries {
		if entry.Status != statusNew {
			continue
		}
		track := in.prepared[entry.SourceID]
		prepared = append(prepared, tokenPrepared{SourceID: entry.SourceID, Metadata: track.metadata, Beat: track.media.beat, Cues: track.media.cues, Loops: track.media.loops, Added: track.added})
	}
	input := struct {
		Review                                    Review
		LibraryUUID, LibraryPath, ArchiveRevision string
		Tracks                                    []tokenTrack
		Prepared                                  []tokenPrepared
		Media                                     []tokenMedia
		Document                                  tokenMedia
	}{review, in.libraryUUID, in.libraryPath, in.archive.Revision(), tracks, prepared, reviewedMedia(in), documentObservation(in.files.document)}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("fingerprinting reviewed import: %w", err)
	}
	return importsource.DocumentID(data), nil
}
