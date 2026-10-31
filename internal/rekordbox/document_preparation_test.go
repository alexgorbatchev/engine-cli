package rekordbox

import "testing"

func TestPrepareReviewRequiresObservedSourceDocument(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	request := preparation{source: src, destination: destinationObservation{uuid: "library"}, library: "/library", options: Options{Path: "/source.xml", Name: "Imported"}, files: mediaObservation{document: mediaFile{path: "/source.xml", key: "source-identity", size: int64(len(src.document.XML))}}}
	for _, name := range []string{"valid", "unobserved", "no identity", "absent", "different path", "different byte count"} {
		t.Run(name, func(t *testing.T) {
			next := request
			switch name {
			case "unobserved":
				next.files.document = mediaFile{}
			case "no identity":
				next.files.document.key = ""
			case "absent":
				next.files.document.key, next.files.document.missing = "", true
			case "different path":
				next.files.document.path = "/other.xml"
			case "different byte count":
				next.files.document.size++
			}
			input, review, err := prepareReview(next)
			if name == "valid" {
				if err != nil || !review.CanApply || review.PlanToken == "" {
					t.Fatalf("observed source document rejected: %+v, %v", review, err)
				}
				return
			}
			if err == nil || input.libraryUUID != "" || review.PlanToken != "" {
				t.Fatalf("unobserved or mismatched source produced consent: %+v, %v", review, err)
			}
		})
	}
}
