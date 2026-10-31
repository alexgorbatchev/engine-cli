package rekordbox

import "maps"

type preparation struct {
	source      source
	destination destinationObservation
	files       mediaObservation
	extents     map[string]audioExtent
	options     Options
	library     string
}

// prepareReview consumes already captured identities and authoritative decoded
// extents. It does no I/O and never decodes inside the later write transaction.
func prepareReview(request preparation) (reviewInput, Review, error) {
	document := request.files.document
	if !validMediaFile(document) || document.missing || document.path != request.options.Path || document.size != int64(len(request.source.document.XML)) {
		return reviewInput{}, Review{}, importError(staleCode, "source XML has no matching present file observation")
	}
	entries, err := classifyEntries(request.source, request.destination, request.files)
	if err != nil {
		return reviewInput{}, Review{}, err
	}
	prepared, err := prepareNewTracks(request.source, entries, request.extents)
	if err != nil {
		return reviewInput{}, Review{}, err
	}
	in := reviewInput{source: request.source, options: request.options, libraryUUID: request.destination.uuid, libraryPath: request.library, archive: request.destination.archive, entries: entries, prepared: prepared, native: request.destination.tracks, files: mediaObservation{document: request.files.document, source: maps.Clone(request.files.source), native: maps.Clone(request.files.native)}}
	review, err := buildReview(in)
	if err != nil {
		return reviewInput{}, Review{}, err
	}
	return in, review, nil
}

func prepareNewTracks(src source, entries []Entry, extents map[string]audioExtent) (map[string]preparedTrack, error) {
	prepared := make(map[string]preparedTrack, len(extents))
	for i, entry := range entries {
		if entry.Status != statusNew {
			continue
		}
		audio, ok := extents[entry.SourceID]
		if !ok {
			return nil, importError(staleCode, "new source track has no authoritative audio extent")
		}
		song := src.library.Songs[i]
		song.Path = entry.Path
		track, err := prepareTrack(src.xml.Collection.Tracks[i], song, audio)
		if err != nil {
			return nil, err
		}
		prepared[entry.SourceID] = track
	}
	if len(prepared) != len(extents) {
		return nil, importError(staleCode, "audio extents include an unreviewed source track")
	}
	return prepared, nil
}
