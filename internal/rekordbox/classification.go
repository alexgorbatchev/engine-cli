package rekordbox

import "time"

// mediaFile is an observation from the qualified native file-identity owner.
// Missing means confirmed absence, not an unreadable or unobserved file. The
// opaque key identifies aliases without relying on path spelling or case.
type mediaFile struct {
	path, key string
	missing   bool
	size      int64
	modified  time.Time
}

type mediaObservation struct {
	document mediaFile
	source   map[string]mediaFile
	native   map[int64]mediaFile
}

// classifyEntries preserves every source selector, including conflicts. An
// archived origin remains authoritative after a move; unrelated native matches
// require explicit reuse consent later. Multiple XML records for one physical
// file are conflicts rather than accidental duplicate native insertions.
func classifyEntries(src source, destination destinationObservation, files mediaObservation) ([]Entry, error) {
	if err := validateMediaObservation(src, destination, files); err != nil {
		return nil, err
	}
	byFile := make(map[string][]nativeTrack, len(destination.tracks))
	for _, track := range destination.tracks {
		file := files.native[track.id]
		if !file.missing {
			byFile[file.key] = append(byFile[file.key], track)
		}
	}
	counts := make(map[string]int, len(files.source))
	for _, file := range files.source {
		if !file.missing {
			counts[file.key]++
		}
	}
	stale := make(map[string]bool, len(destination.review.stale))
	for _, selector := range destination.review.stale {
		stale[selector] = true
	}
	entries := make([]Entry, len(src.selectors))
	for i, selector := range src.selectors {
		file := files.source[selector]
		entry := Entry{SourceID: selector, Path: file.path}
		switch {
		case stale[selector]:
			entry.Status = statusStale
		case !file.missing && counts[file.key] > 1:
			entry.Status = statusConflict
		case destination.review.tracks[selector].id != 0:
			track := destination.review.tracks[selector]
			entry.Status, entry.TrackID, entry.Path = statusRetained, track.id, track.path
		case file.missing:
			entry.Status = statusMissing
		case len(byFile[file.key]) > 1:
			entry.Status = statusConflict
		case len(byFile[file.key]) == 1:
			entry.Status, entry.TrackID = statusExisting, byFile[file.key][0].id
		default:
			entry.Status = statusNew
		}
		entries[i] = entry
	}
	return entries, nil
}

func validateMediaObservation(src source, destination destinationObservation, files mediaObservation) error {
	if len(files.source) != len(src.selectors) || len(files.native) != len(destination.tracks) {
		return importError(staleCode, "media observation does not cover the source and destination")
	}
	selectors := make(map[string]bool, len(src.selectors))
	for _, selector := range src.selectors {
		selectors[selector] = true
		if file, ok := files.source[selector]; !ok || !validMediaFile(file) {
			return importError(staleCode, "source media has no confirmed identity or absence")
		}
	}
	for _, track := range destination.tracks {
		if file, ok := files.native[track.id]; !ok || !validMediaFile(file) {
			return importError(staleCode, "destination media has no confirmed identity or absence")
		}
	}
	for _, selector := range destination.review.stale {
		if !selectors[selector] {
			return importError(staleCode, "source archive contains an unknown stale mapping")
		}
	}
	return nil
}

func validMediaFile(file mediaFile) bool {
	return file.path != "" && (file.missing && file.key == "" || !file.missing && file.key != "")
}
