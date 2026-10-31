package rekordbox

import (
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAcceptReviewPreservesExactDispositionsAndRefusesStaleConsent(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///new.wav"/><TRACK TrackID="2" Location="file:///existing.wav"/><TRACK TrackID="3" Location="file:///missing.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepareTrack(src.xml.Collection.Tracks[0], src.library.Songs[0], audioExtent{rate: 48000, frames: 480000})
	if err != nil {
		t.Fatal(err)
	}
	archive := importsource.State{}
	in := reviewInput{source: src, options: Options{Path: "/source.xml", Name: "New root", MissingMedia: policySkip, Conflicts: policyReuse}, libraryPath: "/library", libraryUUID: "library", archive: archive, entries: []Entry{{SourceID: "id:1", Status: statusNew}, {SourceID: "id:2", Status: statusExisting, TrackID: 91}, {SourceID: "id:3", Status: statusMissing}}, prepared: map[string]preparedTrack{"id:1": p}, native: []nativeTrack{{id: 91, path: "/existing.wav", origin: nativeOrigin{uuid: "origin", id: 4}}}}
	review, err := buildReview(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := acceptReview(in, archive, review.PlanToken)
	if err != nil || got.name != "New root" || got.tracks["id:1"].prepared == nil || got.tracks["id:2"].retained.id != 91 || !got.tracks["id:3"].omit {
		t.Fatalf("reviewed dispositions changed: %+v, %v", got, err)
	}
	for _, name := range []string{"no token", "other token", "changed archive", "changed native observation", "missing target", "unconsented omission"} {
		t.Run(name, func(t *testing.T) {
			next, state, token := in, archive, review.PlanToken
			switch name {
			case "no token":
				token = ""
			case "other token":
				token = "other"
			case "changed archive":
				state.Tracks = []importsource.Track{{SourceID: "other"}}
			case "changed native observation":
				next.native = []nativeTrack{{id: 92}}
			case "missing target":
				next.native = nil
			case "unconsented omission":
				next.options.MissingMedia = policyReject
			}
			// A fresh token still cannot make an invalid target or blocked plan applicable.
			if name == "missing target" || name == "unconsented omission" {
				r, err := buildReview(next)
				if err != nil {
					t.Fatal(err)
				}
				token = r.PlanToken
			}
			got, err := acceptReview(next, state, token)
			if err == nil || got.tracks != nil || (progress.ErrorCode(err) != staleCode && progress.ErrorCode(err) != reviewCode) {
				t.Fatalf("invalid consent produced a native plan: %+v, %v", got, err)
			}
		})
	}
}

func TestAcceptReviewRefusesCompleteDocumentButAllowsReviewedPartialImport(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	track := nativeTrack{id: 91, path: "/a.wav", origin: nativeOrigin{uuid: "origin", id: 4}}
	state := importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{{DocumentID: src.document.ID, SourceID: "id:1", OriginUUID: "origin", OriginTrackID: 4}}}
	for _, complete := range []bool{false, true} {
		archive := state
		if !complete {
			archive.Tracks = nil
		}
		in := reviewInput{source: src, options: Options{Name: "New root", Conflicts: policyReuse}, libraryUUID: "library", libraryPath: "/library", archive: archive, entries: []Entry{{SourceID: "id:1", Status: statusExisting, TrackID: track.id}}, native: []nativeTrack{track}}
		review, err := buildReview(in)
		if err != nil {
			t.Fatal(err)
		}
		if review.CanApply == complete || review.AlreadyImported != complete {
			t.Fatalf("preview advertised an incorrect complete-document disposition: complete=%v review=%+v", complete, review)
		}
		got, err := acceptReview(in, archive, review.PlanToken)
		if complete {
			if err == nil || progress.ErrorCode(err) != reviewCode || got.tracks != nil {
				t.Fatalf("complete document accepted a duplicate root: %+v, %v", got, err)
			}
		} else if err != nil || got.tracks["id:1"].retained.id != track.id {
			t.Fatalf("document presence blocked reviewed partial import: %+v, %v", got, err)
		}
	}
}
