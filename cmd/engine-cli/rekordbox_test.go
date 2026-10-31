package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/rekordbox"
)

func TestRekordboxHumanReviewDisclosesDispositionAndPolicies(t *testing.T) {
	for _, test := range []struct {
		name     string
		canApply bool
		complete bool
		want     string
	}{
		{"applicable", true, false, "Plan token: reviewed-token"},
		{"blocked", false, false, "Not applicable as reviewed:"},
		{"complete", false, true, "already completely mapped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			review := &rekordbox.Review{
				Path: "/music/library.xml", Name: "Imported collection", MissingMedia: "skip", Conflicts: "reuse",
				NewCount: 2, ReuseCount: 1, RetainedCount: 3, MissingCount: 1, AssignedMarkerColor: "#FFFFFF",
				Entries:   []rekordbox.Entry{{SourceID: "document:7", Status: "missing", Path: "/music/missing.flac"}},
				Playlists: []rekordbox.Playlist{{Name: "Sets", Folder: true, Children: []rekordbox.Playlist{{Name: "Warm-up", Tracks: []string{"document:7", "document:8"}}}}},
				Warnings:  []rekordbox.Warning{{Code: "import_source_only", SourceID: "document:8", Field: "Colour"}},
				CanApply:  test.canApply, AlreadyImported: test.complete, PlanToken: "reviewed-token",
			}
			var out bytes.Buffer
			handled, err := printRekordbox(&out, review, false)
			if !handled || err != nil {
				t.Fatalf("review handling: %t %v", handled, err)
			}
			for _, text := range []string{test.want, "New tracks: 2", "Existing tracks to reuse: 1", "Previously mapped tracks to retain: 3", "Unavailable sources: 1", "Unavailable-media policy: skip", "Existing-media policy: reuse", "Assigned cue/loop color: #FFFFFF", "source document:7: missing", "folder: Sets", "playlist: Warm-up (2 source memberships)", "Colour (source document:8); preserved in XML, not imported into Engine"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("review omitted %q: %s", text, &out)
				}
			}
			if !test.canApply && strings.Contains(out.String(), "reviewed-token") {
				t.Fatalf("blocked review advertised apply token: %s", &out)
			}
			if strings.Contains(out.String(), `"plan_token"`) {
				t.Fatalf("human review emitted JSON: %s", &out)
			}
		})
	}
}

func TestRekordboxResultOutputAndStructuredDispatch(t *testing.T) {
	result := &rekordbox.Result{RootID: 19, DocumentID: "source-document", CreatedCount: 2, ReusedCount: 4, SkippedCount: 1}
	var out bytes.Buffer
	handled, err := printRekordbox(&out, result, false)
	if !handled || err != nil || out.String() != "Created playlist root 19: 2 tracks imported, 4 existing tracks reused or retained, 1 unavailable sources skipped.\nPreserved source document: source-document\n" {
		t.Fatalf("result: %t %v %q", handled, err, out.String())
	}
	for _, value := range []any{result, &rekordbox.Review{}, "other operation"} {
		out.Reset()
		if handled, err := printRekordbox(&out, value, true); handled || err != nil || out.Len() != 0 {
			t.Fatalf("structured dispatch consumed output: %t %v %q", handled, err, &out)
		}
	}
	if handled, err := printRekordbox(&out, "other operation", false); handled || err != nil || out.Len() != 0 {
		t.Fatalf("unrelated dispatch consumed output: %t %v %q", handled, err, &out)
	}
}

func TestRekordboxOutputPropagatesClosedWriter(t *testing.T) {
	for _, value := range []any{&rekordbox.Review{}, &rekordbox.Result{RootID: 1}} {
		reader, writer := io.Pipe()
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		handled, err := printRekordbox(writer, value, false)
		if !handled || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("failed output: %t %v", handled, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
