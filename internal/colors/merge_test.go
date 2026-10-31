package colors

import (
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestMergeColorDecisionPreservesUnrelatedOrigins(t *testing.T) {
	keep := companion.Origin{UUID: "library", ID: 1}
	removed := []companion.Origin{{UUID: "library", ID: 2}, {UUID: "library", ID: 3}}
	for _, test := range []struct {
		name, keep, first, second, want, code string
	}{
		{name: "survivor wins", keep: "#000001", first: "#000002", second: "#000003", want: "#000001"},
		{name: "one inherited color", first: "#000002", want: "#000002"},
		{name: "same inherited colors", first: "#000002", second: "#000002", want: "#000002"},
		{name: "ambiguous", first: "#000002", second: "#000003", code: "color_merge_conflict"},
		{name: "unassigned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			library := t.TempDir()
			original := State{Assignments: []Assignment{{OriginUUID: "foreign", OriginTrackID: 2, Color: "#ABCDEF"}}}
			for i, color := range []string{test.keep, test.first, test.second} {
				if color != "" {
					original.Assignments = append(original.Assignments, Assignment{OriginUUID: "library", OriginTrackID: int64(i + 1), Color: color})
				}
			}
			initial, err := Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := Replace(t.Context(), library, initial.Revision(), original)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckMerge(t.Context(), library, keep, removed); progress.ErrorCode(err) != test.code {
				t.Fatalf("preflight error=%v", err)
			}
			err = MergeOrigins(t.Context(), library, keep, removed)
			if progress.ErrorCode(err) != test.code {
				t.Fatalf("merge error=%v", err)
			}
			want := State{Assignments: []Assignment{{OriginUUID: "foreign", OriginTrackID: 2, Color: "#ABCDEF"}}}
			if test.want != "" {
				want.Assignments = append(want.Assignments, Assignment{OriginUUID: keep.UUID, OriginTrackID: keep.ID, Color: test.want})
			}
			if test.code != "" {
				want = stored
			}
			got, err := Read(t.Context(), library)
			if err != nil || !reflect.DeepEqual(got, canonical(want)) {
				t.Fatalf("stored=%+v want=%+v error=%v", got, canonical(want), err)
			}
		})
	}
}
