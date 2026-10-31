package rekordbox

import (
	"bytes"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

const staleCode = "rekordbox_import_stale"

// mergeArchive preserves historical documents and immutable source mappings.
// Omitted source tracks remain unmapped, so partial imports are never mistaken
// for a complete copy of the source collection.
func mergeArchive(src source, state importsource.State, tracks map[string]nativeTrack) (importsource.State, error) {
	next := importsource.State{Documents: slices.Clone(state.Documents), Tracks: slices.Clone(state.Tracks)}
	known := make(map[string]importsource.Track)
	for _, mapping := range state.Tracks {
		if mapping.DocumentID == src.document.ID {
			known[mapping.SourceID] = mapping
		}
	}
	for _, selector := range src.selectors {
		track, ok := tracks[selector]
		if !ok || track.id == 0 {
			continue
		}
		if track.id < 0 || track.origin.uuid == "" || track.origin.id <= 0 {
			return importsource.State{}, importError(staleCode, "imported track has no valid native origin")
		}
		mapping := importsource.Track{DocumentID: src.document.ID, SourceID: selector, OriginUUID: track.origin.uuid, OriginTrackID: track.origin.id}
		if old, exists := known[selector]; exists {
			if old != mapping {
				return importsource.State{}, importError(staleCode, "reviewed source mapping conflicts with retained native identity")
			}
			continue
		}
		next.Tracks = append(next.Tracks, mapping)
	}
	next.Documents = retainDocument(next.Documents, src.document)
	return next, nil
}

func retainDocument(documents []importsource.Document, document importsource.Document) []importsource.Document {
	for _, old := range documents {
		if old.ID == document.ID {
			return documents
		}
	}
	document.XML = bytes.Clone(document.XML)
	return append(documents, document)
}
