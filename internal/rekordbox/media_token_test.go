package rekordbox

import (
	"testing"
	"time"
)

func TestReviewedMediaReplacementInvalidatesOtherwiseIdenticalPlan(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	in := reviewInput{
		source: src, libraryUUID: "library", libraryPath: "/library", options: Options{Name: "Imported", Conflicts: policyReuse},
		entries: []Entry{{SourceID: "id:1", Path: "/a.wav", Status: statusExisting, TrackID: 91}},
		native:  []nativeTrack{{id: 91, path: "/native.wav", origin: nativeOrigin{uuid: "origin", id: 7}}},
		files:   mediaObservation{source: map[string]mediaFile{"id:1": {path: "/a.wav", key: "source-file"}}, native: map[int64]mediaFile{91: {path: "/native.wav", key: "native-file"}}},
	}
	review, err := buildReview(in)
	if err != nil || !review.CanApply {
		t.Fatalf("original plan failed: %+v, %v", review, err)
	}
	for _, name := range []string{"source replacement", "native replacement", "source absent", "native absent"} {
		t.Run(name, func(t *testing.T) {
			next := in
			next.files = mediaObservation{source: map[string]mediaFile{"id:1": in.files.source["id:1"]}, native: map[int64]mediaFile{91: in.files.native[91]}}
			switch name {
			case "source replacement":
				next.files.source["id:1"] = mediaFile{path: "/a.wav", key: "replaced-source"}
			case "native replacement":
				next.files.native[91] = mediaFile{path: "/native.wav", key: "replaced-native"}
			case "source absent":
				next.files.source["id:1"] = mediaFile{path: "/a.wav", missing: true}
			case "native absent":
				next.files.native[91] = mediaFile{path: "/native.wav", missing: true}
			}
			if plan, err := acceptReview(next, in.archive, review.PlanToken); err == nil || plan.tracks != nil {
				t.Fatalf("old token authorized changed physical observation: %+v, %v", plan, err)
			}
		})
	}
	if _, err := acceptReview(in, in.archive, review.PlanToken); err != nil {
		t.Fatalf("unchanged observation rejected: %v", err)
	}
}

func TestReviewedMediaInPlaceChangesInvalidatePlan(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, time.October, 30, 12, 30, 0, 123, time.UTC)
	file := mediaFile{path: "/a.wav", key: "same-file", size: 100, modified: modified}
	in := reviewInput{
		source: src, libraryUUID: "library", libraryPath: "/library", options: Options{Name: "Imported", Conflicts: policyReuse},
		entries: []Entry{{SourceID: "id:1", Path: file.path, Status: statusExisting, TrackID: 91}},
		native:  []nativeTrack{{id: 91, path: file.path, origin: nativeOrigin{uuid: "origin", id: 7}}},
		files:   mediaObservation{source: map[string]mediaFile{"id:1": file}, native: map[int64]mediaFile{91: file}},
	}
	review, err := buildReview(in)
	if err != nil || !review.CanApply {
		t.Fatalf("original plan failed: %+v, %v", review, err)
	}
	for _, target := range []string{"source", "native"} {
		for _, change := range []string{"size", "modification time"} {
			t.Run(target+"/"+change, func(t *testing.T) {
				changed := file
				if change == "size" {
					changed.size++
				} else {
					changed.modified = changed.modified.Add(time.Nanosecond)
				}
				next := in
				next.files = mediaObservation{source: map[string]mediaFile{"id:1": file}, native: map[int64]mediaFile{91: file}}
				if target == "source" {
					next.files.source["id:1"] = changed
				} else {
					next.files.native[91] = changed
				}
				if plan, err := acceptReview(next, in.archive, review.PlanToken); err == nil || plan.tracks != nil {
					t.Fatalf("old token authorized changed file freshness: %+v, %v", plan, err)
				}
			})
		}
	}
	file.modified = modified.In(time.FixedZone("observation", 3600))
	in.files.source["id:1"], in.files.native[91] = file, file
	if _, err := acceptReview(in, in.archive, review.PlanToken); err != nil {
		t.Fatalf("equivalent modification instant rejected: %v", err)
	}
}
