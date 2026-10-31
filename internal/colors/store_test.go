package colors

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestIndependentColorPublicationAndClearing(t *testing.T) {
	library := t.TempDir()
	initial, err := Read(t.Context(), library)
	if err != nil || len(initial.Assignments) != 0 {
		t.Fatalf("initial=%+v error=%v", initial, err)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read enrolled colors: %v", err)
	}
	want := State{Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 10, Color: "#CC3300"}}}
	stored, err := Replace(t.Context(), library, initial.Revision(), want)
	if err != nil || stored.Revision() != want.Revision() {
		t.Fatalf("stored=%+v error=%v", stored, err)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("color write enrolled taxonomy: %v", err)
	}
	_, err = Replace(t.Context(), library, initial.Revision(), initial)
	if progress.ErrorCode(err) != "color_revision_stale" {
		t.Fatalf("stale clearing error=%v", err)
	}
	current, err := Read(t.Context(), library)
	if err != nil || current.Revision() != stored.Revision() {
		t.Fatalf("stale clear changed stored colors: %+v, %v", current, err)
	}
	cleared, err := Replace(t.Context(), library, stored.Revision(), initial)
	if err != nil || len(cleared.Assignments) != 0 {
		t.Fatalf("clear=%+v error=%v", cleared, err)
	}
	other, err := Read(t.Context(), t.TempDir())
	if err != nil || other.Revision() != initial.Revision() {
		t.Fatalf("other library=%+v error=%v", other, err)
	}
}

func TestRejectedAndNoopColorPublicationDoesNotEnroll(t *testing.T) {
	for _, assignment := range []Assignment{
		{OriginUUID: "library-a", OriginTrackID: 1, Color: "#cc3300"},
		{OriginUUID: "", OriginTrackID: 1, Color: "#CC3300"},
		{OriginUUID: "library-a", OriginTrackID: 0, Color: "#CC3300"},
		{OriginUUID: "library-a", OriginTrackID: 1, Color: "red"},
		{OriginUUID: "library-a", OriginTrackID: 1, Color: ""},
	} {
		t.Run(assignment.OriginUUID+assignment.Color, func(t *testing.T) {
			library := t.TempDir()
			initial, err := Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Replace(t.Context(), library, initial.Revision(), State{Assignments: []Assignment{assignment}})
			if progress.ErrorCode(err) != "color_invalid" {
				t.Fatalf("invalid assignment error=%v", err)
			}
			if _, err := os.Stat(companion.ColorsPath(library)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected write enrolled colors: %v", err)
			}
			if _, err := Replace(t.Context(), library, initial.Revision(), initial); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(companion.ColorsPath(library)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("no-op enrolled colors: %v", err)
			}
		})
	}
}

func TestDamagedColorStorageIsNotEmpty(t *testing.T) {
	library := t.TempDir()
	path := companion.ColorsPath(library)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Read(t.Context(), library)
	if progress.ErrorCode(err) != "color_store_unavailable" {
		t.Fatalf("damaged store error=%v", err)
	}
}
