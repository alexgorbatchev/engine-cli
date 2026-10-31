package rekordbox

import "github.com/alexgorbatchev/engine-cli/internal/importsource"

const reviewCode = "rekordbox_import_review_required"

// acceptReview converts only the exact applicable observation into dispositions
// consumed by the native writer. It performs no decoding or collection writes.
func acceptReview(in reviewInput, archive importsource.State, token string) (nativePlan, error) {
	review, err := buildReview(in)
	if err != nil {
		return nativePlan{}, err
	}
	if token == "" || token != review.PlanToken {
		return nativePlan{}, importError(staleCode, "review the current import plan before applying")
	}
	if archive.Revision() != in.archive.Revision() {
		return nativePlan{}, importError(staleCode, "source archive changed after review")
	}
	if review.AlreadyImported {
		return nativePlan{}, importError(reviewCode, "this exact document is already completely mapped in the library")
	}
	if !review.CanApply {
		return nativePlan{}, importError(reviewCode, "resolve conflicts and explicitly review omissions, reuse and source-only fields")
	}
	byID := make(map[int64]nativeTrack, len(in.native))
	for _, track := range in.native {
		byID[track.id] = track
	}
	tracks := make(map[string]trackDisposition, len(in.entries))
	for _, entry := range in.entries {
		var disposition trackDisposition
		switch entry.Status {
		case statusNew:
			prepared := in.prepared[entry.SourceID]
			disposition.prepared = &prepared
		case statusExisting, statusRetained:
			track, ok := byID[entry.TrackID]
			if !ok || track.id <= 0 || track.origin.uuid == "" || track.origin.id <= 0 {
				return nativePlan{}, importError(staleCode, "reviewed native target has no current origin")
			}
			disposition.retained = track
		case statusMissing:
			disposition.omit = true
		default:
			return nativePlan{}, importError(reviewCode, "source track has no applicable reviewed disposition")
		}
		tracks[entry.SourceID] = disposition
	}
	return nativePlan{source: in.source, name: in.options.Name, archive: archive, tracks: tracks}, nil
}
