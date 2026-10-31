package transfer

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSyncColorsReuseDestinationOriginAndResolveConflict(t *testing.T) {
	for _, choice := range []string{"error", "source", "destination"} {
		t.Run(choice, func(t *testing.T) {
			source, s := library(t, "source")
			destination, d := library(t, "destination")
			execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Source','one.mp3','one.mp3')`)
			execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Curated','other.mp3','other.mp3')`)
			for path, name := range map[string]string{source: "one.mp3", destination: "other.mp3"} {
				put(t, filepath.Join(path, name), "identical audio")
			}
			for _, fixture := range []struct{ path, uuid, color string }{{source, "source", "#112233"}, {destination, "destination", "#445566"}} {
				state := colors.State{Assignments: []colors.Assignment{{OriginUUID: fixture.uuid, OriginTrackID: 1, Color: fixture.color}}}
				if _, err := colors.Replace(t.Context(), fixture.path, transferColors(t, fixture.path).Revision(), state); err != nil {
					t.Fatal(err)
				}
			}
			o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, Conflict: choice, DryRun: true, NoBackup: true}
			preview, err := Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			if choice == "error" {
				if preview.Applicable || len(preview.Conflicts) != 1 || preview.Conflicts[0].Entity != "color_assignment" {
					t.Fatalf("missing reviewed color conflict: %+v", preview)
				}
				return
			}
			o.DryRun, o.PlanToken = false, preview.PlanToken
			if _, err := Run(t.Context(), o); err != nil {
				t.Fatal(err)
			}
			want := "#112233"
			if choice == "destination" {
				want = "#445566"
			}
			state := transferColors(t, destination)
			if len(state.Assignments) != 1 || state.Assignments[0] != (colors.Assignment{OriginUUID: "destination", OriginTrackID: 1, Color: want}) {
				t.Fatalf("reused color identity=%+v", state)
			}
			var count int
			var title string
			if err := d.QueryRow("SELECT count(*),title FROM Track").Scan(&count, &title); err != nil || count != 1 || title != "Curated" {
				t.Fatalf("audio reuse changed Engine metadata: count=%d title=%s error=%v", count, title, err)
			}
		})
	}
}

func TestSyncColorsRefusesUnavailableOriginsWithoutPruning(t *testing.T) {
	source, _ := library(t, "source")
	destination, _ := library(t, "destination")
	state := colors.State{Assignments: []colors.Assignment{{OriginUUID: "source", OriginTrackID: 99, Color: "#112233"}}}
	if _, err := colors.Replace(t.Context(), source, transferColors(t, source).Revision(), state); err != nil {
		t.Fatal(err)
	}
	_, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true})
	if progress.ErrorCode(err) != "color_assignments_stale" || transferColors(t, source).Revision() != state.Revision() {
		t.Fatalf("unavailable origin was not retained/rejected: %v", err)
	}
}
