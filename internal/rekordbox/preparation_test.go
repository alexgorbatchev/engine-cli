package rekordbox

import "testing"

func TestPrepareReviewPreservesOrderedDispositionsAndCapturedMedia(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="2" Name="New" Location="file:///xml/new.wav"/><TRACK TrackID="1" Name="Missing" Location="file:///xml/missing.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	request := preparation{
		source: src, destination: destinationObservation{uuid: "native-library"},
		files:   mediaObservation{document: mediaFile{path: "/source.xml", key: "xml-file", size: int64(len(src.document.XML))}, source: map[string]mediaFile{"id:2": {path: "/captured/new.wav", key: "native-file"}, "id:1": {path: "/xml/missing.wav", missing: true}}},
		extents: map[string]audioExtent{"id:2": {rate: 48000, frames: 480000, bytes: 960000}},
		options: Options{Path: "/source.xml", Name: "Imported", MissingMedia: policySkip}, library: "/library",
	}
	for _, name := range []string{"valid", "missing extent", "invalid extent", "unreviewed extent"} {
		t.Run(name, func(t *testing.T) {
			next := request
			next.extents = map[string]audioExtent{"id:2": request.extents["id:2"]}
			switch name {
			case "missing extent":
				delete(next.extents, "id:2")
			case "invalid extent":
				next.extents["id:2"] = audioExtent{}
			case "unreviewed extent":
				next.extents["id:1"] = next.extents["id:2"]
			}
			input, review, err := prepareReview(next)
			if name != "valid" {
				if err == nil || input.prepared != nil || review.PlanToken != "" {
					t.Fatalf("invalid preparation returned partial review: %+v, %+v, %v", input, review, err)
				}
				return
			}
			if err != nil || !review.CanApply || review.NewCount != 1 || review.MissingCount != 1 || review.PlanToken == "" {
				t.Fatalf("review assembly failed: %+v, %v", review, err)
			}
			track := input.prepared["id:2"]
			if len(input.prepared) != 1 || track.metadata.Path.String != "/captured/new.wav" || track.metadata.Length.Int64 != 10 || review.Entries[0].SourceID != "id:2" || review.Entries[1].Status != statusMissing {
				t.Fatalf("captured extent/path or source order lost: %+v, %+v", track, review.Entries)
			}
			if src.library.Songs[0].Path != "/xml/new.wav" {
				t.Fatal("preparation changed parsed source")
			}
			replacedDocument := next
			replacedDocument.files.document.key = "replacement-xml"
			_, documentReview, err := prepareReview(replacedDocument)
			if err != nil || documentReview.PlanToken == review.PlanToken {
				t.Fatalf("preparation lost the XML file observation: %+v, %v", documentReview, err)
			}
			replacement := request
			replacement.files.source = map[string]mediaFile{"id:2": {path: "/captured/new.wav", key: "replaced-file"}, "id:1": request.files.source["id:1"]}
			changed, current, err := prepareReview(replacement)
			if err != nil || current.PlanToken == review.PlanToken {
				t.Fatalf("identical extent concealed physical replacement: %+v, %v", current, err)
			}
			if _, err := acceptReview(changed, input.archive, review.PlanToken); err == nil {
				t.Fatal("old prepared token accepted replacement media")
			}
			original := next.files.source["id:2"]
			delete(next.files.source, "id:2")
			defer func() { next.files.source["id:2"] = original }()
			if _, err := acceptReview(input, input.archive, review.PlanToken); err != nil {
				t.Fatalf("caller mutation changed retained media observations: %v", err)
			}
		})
	}
}
