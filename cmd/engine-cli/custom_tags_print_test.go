package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

// failingWriter accepts the first ok writes, then refuses every later one.
type failingWriter struct{ ok int }

var errWriteRefused = errors.New("write refused")

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.ok <= 0 {
		return 0, errWriteRefused
	}
	w.ok--
	return len(p), nil
}

func customTagsPrintResults() map[string]any {
	taxonomy := tags.Taxonomy{
		Revision:          "r2",
		UnavailableTracks: 1,
		Categories: []tags.Category{
			{ID: "energy", Name: "Energy", Mode: tags.ModeSingle, Values: []tags.Value{{ID: "low", Name: "Low", TrackCount: 2}}},
		},
	}
	return map[string]any{
		"collection": &tags.CollectionTags{Taxonomy: &taxonomy, Tracks: []tags.TrackValues{{TrackID: 4, Values: []string{"low"}}}},
		"taxonomy":   &taxonomy,
		"track":      &tags.TrackTags{TrackID: 4, Revision: "r2", Values: []string{"low"}},
		"batch": &tags.BatchTags{Revision: "r2", TrackIDs: []int64{4, 5}, Categories: []tags.BatchCategory{
			{ID: "energy", Name: "Energy", Values: []tags.BatchValue{{ID: "low", Name: "Low", SelectedTracks: 1}}},
		}},
		"change":         &tags.BatchChange{Revision: "r2", TrackIDs: []int64{4}, Add: []string{"high"}, Remove: []string{}, Replaced: []string{"low"}, PlanToken: "plan", ChangedTracks: 1, AddedAssignments: 1, RemovedAssignments: 1},
		"mutation":       &tags.Mutation{ID: "energy", Revision: "r3", Changed: true},
		"deletion":       &tags.Deletion{ID: "low", Revision: "r2", PlanToken: "plan", AffectedTracks: 2, RemovedAssignments: 2, RemovedValues: 1},
		"merge":          &tags.Merge{ID: "hypnotc", Into: "hypnotic", Revision: "r2", PlanToken: "plan", AffectedTracks: 3, MovedAssignments: 2},
		"reconciliation": &tags.Reconciliation{Revision: "r2", PlanToken: "plan", UnavailableTracks: 1, RemovedAssignments: 1},
	}
}

func TestPrintCustomTagsStatesModesAndReplacedValues(t *testing.T) {
	want := map[string][]string{
		"collection":     {"Custom tags: 1 categories", "Assigned tracks: 1", "Track 4: low"},
		"taxonomy":       {"Energy [energy] — single choice", "  Low [low] — 2 tracks"},
		"track":          {"Track: 4", "Custom values: low"},
		"batch":          {"Selected tracks: [4 5]", "  Low [low] — 1 of 2 selected tracks"},
		"change":         {"Add values: high", "Replaced values: low", "Removed assignments: 1"},
		"mutation":       {"Custom tag: energy", "Changed: true", "Revision: r3"},
		"deletion":       {"Affected tracks: 2", "Removed values: 1"},
		"reconciliation": {"Unavailable tracks: 1", "Plan token: plan"},
		"merge":          {"Merged value: hypnotc", "Into value: hypnotic", "Moved assignments: 2"},
	}
	for name, result := range customTagsPrintResults() {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&output)
			handled, err := printCustomTags(command, result)
			if !handled || err != nil {
				t.Fatalf("printCustomTags(%s) handled=%t error=%v", name, handled, err)
			}
			for _, line := range want[name] {
				if !strings.Contains(output.String(), line+"\n") {
					t.Fatalf("output lacks %q:\n%s", line, output.String())
				}
			}
		})
	}
	if handled, err := printCustomTags(&cobra.Command{}, "other"); handled || err != nil {
		t.Fatalf("an unrelated result was handled=%t error=%v", handled, err)
	}
}

func TestPrintCustomTagsReportsEveryFailedWrite(t *testing.T) {
	for name, result := range customTagsPrintResults() {
		for ok := 0; ok < 4; ok++ {
			command := &cobra.Command{}
			command.SetOut(&failingWriter{ok: ok})
			if handled, err := printCustomTags(command, result); !handled {
				t.Fatalf("%s after %d writes was not handled", name, ok)
			} else if (ok == 0 && err == nil) || (err != nil && !errors.Is(err, errWriteRefused)) {
				t.Fatalf("%s after %d writes reported %v", name, ok, err)
			}
		}
	}
}
