package rekordbox

import (
	"testing"
	"time"
)

func TestReviewedDocumentReplacementInvalidatesUnchangedXML(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	in := reviewInput{source: src, libraryUUID: "library", libraryPath: "/library", options: Options{Path: "/source.xml", Name: "Imported", Conflicts: policyReuse}, entries: []Entry{{SourceID: "id:1", Path: "/a.wav", Status: statusExisting, TrackID: 91}}, native: []nativeTrack{{id: 91, path: "/a.wav", origin: nativeOrigin{uuid: "origin", id: 1}}}}
	in.files.document = mediaFile{path: "/source.xml", key: "xml-file", size: int64(len(in.source.document.XML)), modified: time.Date(2026, time.October, 30, 12, 0, 0, 123, time.UTC)}
	review, err := buildReview(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"identity", "path", "size", "modified", "absence"} {
		t.Run(change, func(t *testing.T) {
			next := in
			switch change {
			case "identity":
				next.files.document.key = "replacement-xml"
			case "path":
				next.files.document.path = "/moved.xml"
			case "size":
				next.files.document.size++
			case "modified":
				next.files.document.modified = next.files.document.modified.Add(time.Nanosecond)
			case "absence":
				next.files.document.missing = true
				next.files.document.key = ""
			}
			if plan, err := acceptReview(next, in.archive, review.PlanToken); err == nil || plan.tracks != nil {
				t.Fatalf("unchanged XML authorized a changed source file: %+v, %v", plan, err)
			}
		})
	}
	in.files.document.modified = in.files.document.modified.In(time.FixedZone("equivalent", 3600))
	if _, err := acceptReview(in, in.archive, review.PlanToken); err != nil {
		t.Fatalf("equivalent source modification instant rejected: %v", err)
	}
}
