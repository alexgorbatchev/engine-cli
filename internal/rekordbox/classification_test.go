package rekordbox

import (
	"slices"
	"testing"
)

func TestClassifyEntriesPreservesOrderAndExplicitMediaDispositions(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="2" Location="file:///first.wav"/><TRACK TrackID="1" Location="file:///second.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"new", "existing alias", "duplicate source aliases", "ambiguous native aliases", "missing", "retained moved origin", "stale origin", "missing native media"} {
		t.Run(name, func(t *testing.T) {
			track := nativeTrack{id: 91, path: "/moved.wav", origin: nativeOrigin{uuid: "origin", id: 4}}
			destination := destinationObservation{tracks: []nativeTrack{track}, review: archiveReview{tracks: map[string]nativeTrack{}}}
			files := mediaObservation{source: map[string]mediaFile{"id:2": {path: "/first.wav", key: "file-a"}, "id:1": {path: "/second.wav", key: "file-b"}}, native: map[int64]mediaFile{91: {path: track.path, key: "native-file"}}}
			want := []Entry{{SourceID: "id:2", Path: "/first.wav", Status: statusNew}, {SourceID: "id:1", Path: "/second.wav", Status: statusNew}}
			switch name {
			case "existing alias":
				files.native[91] = mediaFile{path: track.path, key: "file-a"}
				want[0].Status, want[0].TrackID = statusExisting, 91
			case "duplicate source aliases":
				files.source["id:1"] = mediaFile{path: "/different-hard-link.wav", key: "file-a"}
				want[0].Status, want[1].Status, want[1].Path = statusConflict, statusConflict, "/different-hard-link.wav"
			case "ambiguous native aliases":
				destination.tracks = append(destination.tracks, nativeTrack{id: 92})
				files.native[91] = mediaFile{path: "/native-one.wav", key: "file-a"}
				files.native[92] = mediaFile{path: "/native-two.wav", key: "file-a"}
				want[0].Status = statusConflict
			case "missing":
				files.source["id:2"] = mediaFile{path: "/first.wav", missing: true}
				want[0].Status = statusMissing
			case "retained moved origin":
				files.source["id:2"] = mediaFile{path: "/first.wav", missing: true}
				destination.review.tracks["id:2"] = track
				want[0].Status, want[0].TrackID, want[0].Path = statusRetained, 91, track.path
			case "stale origin":
				destination.review.stale = []string{"id:2"}
				want[0].Status = statusStale
			case "missing native media":
				files.native[91] = mediaFile{path: track.path, missing: true}
			}
			got, err := classifyEntries(src, destination, files)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("dispositions lost order/identity: got=%+v want=%+v error=%v", got, want, err)
			}
		})
	}
}

func TestClassifyEntriesRefusesIncompleteOrInconsistentMediaObservations(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"source count", "source selector", "source identity", "source path", "missing with identity", "native count", "native selector", "native identity", "unknown stale mapping"} {
		t.Run(name, func(t *testing.T) {
			destination := destinationObservation{tracks: []nativeTrack{{id: 91}}}
			files := mediaObservation{source: map[string]mediaFile{"id:1": {path: "/a.wav", key: "a"}}, native: map[int64]mediaFile{91: {path: "/native.wav", key: "b"}}}
			switch name {
			case "source count":
				files.source = nil
			case "source selector":
				files.source = map[string]mediaFile{"unknown": {path: "/a.wav", key: "a"}}
			case "source identity":
				files.source["id:1"] = mediaFile{path: "/a.wav"}
			case "source path":
				files.source["id:1"] = mediaFile{key: "a"}
			case "missing with identity":
				files.source["id:1"] = mediaFile{path: "/a.wav", key: "a", missing: true}
			case "native count":
				files.native = nil
			case "native selector":
				files.native = map[int64]mediaFile{92: {path: "/native.wav", key: "b"}}
			case "native identity":
				files.native[91] = mediaFile{path: "/native.wav"}
			case "unknown stale mapping":
				destination.review.stale = []string{"unknown"}
			}
			got, err := classifyEntries(src, destination, files)
			if err == nil || got != nil {
				t.Fatalf("unobserved media became an import disposition: %+v, %v", got, err)
			}
		})
	}
}
