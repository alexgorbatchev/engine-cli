package rekordbox

import (
	"math"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestBuildReviewBindsPoliciesPreparedBytesAndDestination(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="0" Name="New" Grouping="Archive only" Location="file:///a.wav"><POSITION_MARK Type="0" Start="1.25" Num="0"/></TRACK><TRACK TrackID="2" Location="file:///b.wav"/><TRACK TrackID="3" Location="file:///missing.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTrack(src.xml.Collection.Tracks[0], src.library.Songs[0], audioExtent{rate: 48000, frames: 480000})
	if err != nil {
		t.Fatal(err)
	}
	in := reviewInput{source: src, options: Options{Path: "/source.xml", Name: "Root", MissingMedia: policySkip, Conflicts: policyReuse, AllowUnsupported: true}, libraryUUID: "library-a", libraryPath: "/library-a", archive: importsource.State{}, entries: []Entry{{SourceID: "id:0", Path: "/a.wav", Status: statusNew}, {SourceID: "id:2", Path: "/b.wav", Status: statusExisting, TrackID: 91}, {SourceID: "id:3", Path: "/missing.wav", Status: statusMissing}}, prepared: map[string]preparedTrack{"id:0": prepared}, native: []nativeTrack{{id: 91, path: "/b.wav", origin: nativeOrigin{uuid: "origin", id: 4}}}}
	got, err := buildReview(in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CanApply || got.NewCount != 1 || got.ReuseCount != 1 || got.MissingCount != 1 || got.AssignedMarkerColor != "#FFFFFF" || len(got.Warnings) != 1 || got.Warnings[0].Field != "Grouping" || got.PlanToken == "" {
		t.Fatalf("incomplete review: %+v", got)
	}
	for _, name := range []string{"missing consent", "reuse consent", "source-only consent", "destination", "library path", "archive", "root name", "prepared bytes", "native origin", "source bytes"} {
		t.Run(name, func(t *testing.T) {
			next := in
			switch name {
			case "missing consent":
				next.options.MissingMedia = policyReject
			case "reuse consent":
				next.options.Conflicts = policyReject
			case "source-only consent":
				next.options.AllowUnsupported = false
			case "destination":
				next.libraryUUID = "other-library"
			case "library path":
				next.libraryPath = "/cloned-library"
			case "archive":
				next.archive.Documents = []importsource.Document{{ID: "other-document"}}
			case "root name":
				next.options.Name = "Other root"
			case "prepared bytes":
				p := prepared
				p.media.cues = []byte("other prepared bytes")
				next.prepared = map[string]preparedTrack{"id:0": p}
			case "native origin":
				next.native = []nativeTrack{{id: 91, path: "/b.wav", origin: nativeOrigin{uuid: "other-origin", id: 4}}}
			case "source bytes":
				next.source.document.ID = "other-source"
			}
			changed, err := buildReview(next)
			if err != nil {
				t.Fatal(err)
			}
			if changed.PlanToken == got.PlanToken {
				t.Fatal("changed reviewed input retained token")
			}
			if (name == "missing consent" || name == "reuse consent" || name == "source-only consent") && changed.CanApply {
				t.Fatal("unconsented loss/reuse remains applicable")
			}
		})
	}
	// Transport-only fields must not make a reviewed dry run unusable at apply.
	in.options.DryRun = true
	in.options.PlanToken = "supplied-token"
	again, err := buildReview(in)
	if err != nil || again.PlanToken != got.PlanToken {
		t.Fatalf("transport flags changed reviewed identity: %+v, %v", again, err)
	}
}

func TestBuildReviewRejectsIncompleteObservationsAndKeepsConflictsExplicit(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{statusRetained, statusConflict, statusStale, statusMissing} {
		in := reviewInput{source: src, libraryUUID: "library", libraryPath: "/library", options: Options{Name: "Root", MissingMedia: policySkip, Conflicts: policyReuse, AllowUnsupported: true}, entries: []Entry{{SourceID: "id:1", Status: status, TrackID: 12}}}
		got, err := buildReview(in)
		if err != nil {
			t.Fatal(err)
		}
		if got.CanApply != (status == statusRetained || status == statusMissing) {
			t.Fatalf("explicit status incorrectly became applicable: %+v", got)
		}
		if status == statusRetained && (got.RetainedCount != 1 || got.ReuseCount != 0) {
			t.Fatalf("archived origin treated as unrelated reuse: %+v", got)
		}
	}
	for _, name := range []string{"no destination", "missing entry", "wrong source identity", "unknown status", "unprepared new track", "missing native target", "unserializable prepared data"} {
		t.Run(name, func(t *testing.T) {
			in := reviewInput{source: src, libraryUUID: "library", libraryPath: "/library", entries: []Entry{{SourceID: "id:1", Status: statusRetained, TrackID: 12}}}
			switch name {
			case "no destination":
				in.libraryUUID = ""
			case "missing entry":
				in.entries = nil
			case "wrong source identity":
				in.entries[0].SourceID = "id:2"
			case "unknown status":
				in.entries[0].Status = "silently-ignore"
			case "unprepared new track":
				in.entries[0].Status = statusNew
			case "missing native target":
				in.entries[0].TrackID = 0
			case "unserializable prepared data":
				p := preparedTrack{}
				p.metadata.BPMAnalyzed.Float64 = math.NaN()
				p.metadata.BPMAnalyzed.Valid = true
				in.entries[0].Status = statusNew
				in.prepared = map[string]preparedTrack{"id:1": p}
			}
			got, err := buildReview(in)
			if err == nil || got.PlanToken != "" || got.Entries != nil {
				t.Fatalf("invalid observation produced partial review: %+v, %v", got, err)
			}
		})
	}
}
