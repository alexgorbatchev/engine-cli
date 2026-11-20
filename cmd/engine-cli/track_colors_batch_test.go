package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
)

func TestTrackColorBatchCommandReviewsThenAppliesThePlan(t *testing.T) {
	library := createTestLibrary(t)
	var initial colors.Collection
	if err := json.Unmarshal([]byte(executeTrackColorJSON(t, "--lib-dir", library, "track", "color", "list", "--json")), &initial); err != nil {
		t.Fatal(err)
	}
	output, err := executeCommand("--lib-dir", library, "track", "color", "batch-update", "--tracks", "1", "--color", "#ffa500", "--revision", initial.Revision, "--dry-run")
	if err != nil || !strings.Contains(output, "Color: #FFA500\n") || !strings.Contains(output, "Track 1: none -> #FFA500\n") || !strings.Contains(output, "Applied: false\n") {
		t.Fatalf("human review=%s error=%v", output, err)
	}
	var plan colors.BatchChange
	if err := json.Unmarshal([]byte(executeTrackColorJSON(t, "--lib-dir", library, "track", "color", "batch-update", "--tracks", "1", "--color", "#ffa500", "--revision", initial.Revision, "--dry-run", "--json")), &plan); err != nil {
		t.Fatal(err)
	}
	output, err = executeCommand("--lib-dir", library, "--no-backup", "track", "color", "batch-update", "--tracks", "1", "--color", "#ffa500", "--revision", initial.Revision, "--plan-token", plan.PlanToken)
	if err != nil || !strings.Contains(output, "Applied: true\n") || !strings.Contains(output, "Changed tracks: 1\n") {
		t.Fatalf("human apply=%s error=%v", output, err)
	}
}

func TestPrintTrackColorBatchNamesClearsAndReportsFailedWrites(t *testing.T) {
	change := &colors.BatchChange{Revision: "r", TrackIDs: []int64{1}, PlanToken: "plan", ChangedTracks: 1, ClearedTracks: 1, Tracks: []colors.BatchTrack{{TrackID: 1, Before: "#FF0000"}}}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	if handled, err := printTrackColors(command, change); !handled || err != nil || !strings.Contains(output.String(), "Color: cleared\n") || !strings.Contains(output.String(), "Track 1: #FF0000 -> none\n") {
		t.Fatalf("handled=%t error=%v output=%s", handled, err, output.String())
	}
	for ok := 0; ok < 2; ok++ {
		command.SetOut(&failingWriter{ok: ok})
		if _, err := printTrackColors(command, change); err == nil {
			t.Fatalf("a failed write after %d writes was not reported", ok)
		}
	}
}
