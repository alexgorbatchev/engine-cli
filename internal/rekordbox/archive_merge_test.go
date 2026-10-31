package rekordbox

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestMergeArchivePreservesHistoryAndOnlyAddsImportedOrigins(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/><TRACK TrackID="2" Location="file:///b.wav"/><TRACK TrackID="3" Location="file:///missing.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	old, err := importsource.NewDocument([]byte(`<DJ_PLAYLISTS><!-- historical source --><COLLECTION><TRACK TrackID="91" Location="file:///historical.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	mapping := importsource.Track{DocumentID: old.ID, SourceID: "id:91", OriginUUID: "existing", OriginTrackID: 91}
	state := importsource.State{Documents: []importsource.Document{old}, Tracks: []importsource.Track{mapping}}
	before := state.Revision()
	tracks := map[string]nativeTrack{
		"id:1": {id: 1, origin: nativeOrigin{uuid: "existing", id: 91}},
		"id:2": {id: 2, origin: nativeOrigin{uuid: "new", id: 2}},
		"id:3": {},
	}
	got, err := mergeArchive(src, state, tracks)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision() != before || len(got.Documents) != 2 || !bytes.Equal(got.Documents[0].XML, old.XML) || len(got.Tracks) != 3 || got.Tracks[0] != mapping || got.Tracks[1].SourceID != "id:1" || got.Tracks[1].OriginUUID != "existing" || got.Tracks[2].SourceID != "id:2" || got.Tracks[2].OriginUUID != "new" || got.Tracks[2].OriginTrackID != 2 {
		t.Fatalf("source evidence lost or omitted media enrolled: %+v", got)
	}
	again, err := mergeArchive(src, got, tracks)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("unchanged mapping duplicated: %+v, %v", again, err)
	}
	exact := bytes.Clone(got.Documents[1].XML)
	src.document.XML[0] = '!'
	if !bytes.Equal(got.Documents[1].XML, exact) {
		t.Fatal("archive retained mutable parsed source bytes")
	}
}

func TestMergeArchiveRejectsReplacementOrInvalidNativeOriginsAtomically(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for name, track := range map[string]nativeTrack{
		"replacement origin":  {id: 2, origin: nativeOrigin{uuid: "changed", id: 2}},
		"missing origin":      {id: 2},
		"negative origin":     {id: 2, origin: nativeOrigin{uuid: "existing", id: -1}},
		"negative native row": {id: -1, origin: nativeOrigin{uuid: "existing", id: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			state := importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{{DocumentID: src.document.ID, SourceID: "id:1", OriginUUID: "existing", OriginTrackID: 1}}}
			before := state.Revision()
			got, err := mergeArchive(src, state, map[string]nativeTrack{"id:1": track})
			if progress.ErrorCode(err) != staleCode || got.Documents != nil || got.Tracks != nil || state.Revision() != before {
				t.Fatalf("conflict partially replaced evidence: %+v, %v", got, err)
			}
		})
	}
}
