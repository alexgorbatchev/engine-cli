package rekordbox

import "time"

type tokenMedia struct {
	SourceID string
	TrackID  int64
	Path     string
	Key      string
	Missing  bool
	Size     int64
	Modified time.Time
}

// XML content and the file containing it are separate observations. Replacing
// the reviewed file with byte-identical XML still requires a fresh review.
func documentObservation(file mediaFile) tokenMedia {
	return tokenMedia{Path: file.path, Key: file.key, Missing: file.missing, Size: file.size, Modified: file.modified.UTC()}
}

// reviewedMedia binds physical observations separately from prepared values:
// replacement audio can have the same metadata and decoded extent.
func reviewedMedia(in reviewInput) []tokenMedia {
	files := make([]tokenMedia, 0, len(in.files.source)+len(in.files.native))
	for _, selector := range in.source.selectors {
		if file, ok := in.files.source[selector]; ok {
			files = append(files, tokenMedia{SourceID: selector, Path: file.path, Key: file.key, Missing: file.missing, Size: file.size, Modified: file.modified.UTC()})
		}
	}
	for _, track := range in.native {
		if file, ok := in.files.native[track.id]; ok {
			files = append(files, tokenMedia{TrackID: track.id, Path: file.path, Key: file.key, Missing: file.missing, Size: file.size, Modified: file.modified.UTC()})
		}
	}
	return files
}
