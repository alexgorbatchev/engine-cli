package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagsBatchNoChangeCommandHasNoRecoverySideEffects(t *testing.T) {
	library, d, before := customTagsCommandLibrary(t)
	args := []string{"--lib-dir", library, "track", "custom-tags", "batch-update", "--tracks", "1", "--revision", before.Revision, "--add", "warm", "--remove=", "--json"}
	output := executeCustomTagsJSON(t, append(args, "--dry-run")...)
	var preview tags.BatchChange
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.ChangedTracks != 0 {
		t.Fatalf("no-change preview=%+v", preview)
	}
	output, err := executeCommand(append(args, "--plan-token", preview.PlanToken)...)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("selected tracks already have")) {
		t.Fatalf("no-change apply=%s error=%v", output, err)
	}
	for _, name := range []string{"backups", "history"} {
		if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
			t.Fatalf("no-change command created %s: %v", name, err)
		}
	}
	after, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || after.Revision != before.Revision || len(after.Values) != 1 {
		t.Fatalf("rejected no-change apply changed assignments: %+v error=%v", after, err)
	}
}

func customTagsCommandLibrary(t *testing.T) (string, *sql.DB, *tags.TrackTags) {
	t.Helper()
	library := createTestLibrary(t)
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("UPDATE Track SET originDatabaseUuid='fixture',originTrackId=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	state, err := tags.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	seed := tags.State{Categories: []tags.Category{{ID: "mood", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}}}}, Assignments: []tags.Assignment{{OriginUUID: "fixture", OriginTrackID: 1, ValueID: "warm"}}}
	if _, err := tags.Replace(t.Context(), library, state.Revision(), seed); err != nil {
		t.Fatal(err)
	}
	before, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil {
		t.Fatal(err)
	}
	return library, d, before
}

func TestCustomTagsNativeCommandClearRequiresExplicitValues(t *testing.T) {
	library, d, before := customTagsCommandLibrary(t)
	args := []string{"--lib-dir", library, "--no-backup", "track", "custom-tags", "update", "1", "--revision", before.Revision, "--json"}
	if output, err := executeCommand(args...); err == nil {
		t.Fatalf("missing values silently cleared assignments: %s", output)
	}
	unchanged, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || len(unchanged.Values) != 1 || unchanged.Revision != before.Revision {
		t.Fatalf("rejected command changed assignments: %+v %v", unchanged, err)
	}
	var output, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(append(args, "--values="))
	if err := command.Execute(); err != nil {
		t.Fatalf("explicit empty values: %v\n%s", err, diagnostics.String())
	}
	var cleared tags.TrackTags
	if err := json.Unmarshal(output.Bytes(), &cleared); err != nil {
		t.Fatalf("decoding command result: %v\n%s", err, output.String())
	}
	if cleared.Values == nil || len(cleared.Values) != 0 || cleared.Revision == before.Revision {
		t.Fatalf("explicit clear result=%+v", cleared)
	}
	stored, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || len(stored.Values) != 0 || stored.Revision != cleared.Revision {
		t.Fatalf("command did not commit clear: %+v %v", stored, err)
	}
}

func TestCustomTagsBatchNativeCommandsRequireReviewAndExplicitActions(t *testing.T) {
	library, d, before := customTagsCommandLibrary(t)
	output, err := executeCommand("--lib-dir", library, "track", "custom-tags", "batch-inspect", "--tracks", "1,1")
	if err != nil || !bytes.Contains([]byte(output), []byte("Warm [warm] — 1 of 1 selected tracks")) {
		t.Fatalf("human mixed assignment inspection=%s error=%v", output, err)
	}
	args := []string{"--lib-dir", library, "--no-backup", "track", "custom-tags", "batch-update", "--tracks", "1,1", "--revision", before.Revision, "--add=", "--remove", "warm", "--json"}
	if output, err := executeCommand(args...); err == nil {
		t.Fatalf("unreviewed batch changed assignments: %s", output)
	}
	if output, err := executeCommand("--lib-dir", library, "track", "custom-tags", "batch-update", "--tracks", "1", "--revision", before.Revision, "--add=", "--dry-run", "--json"); err == nil {
		t.Fatalf("missing remove action list accepted: %s", output)
	}
	output = executeCustomTagsJSON(t, append(args, "--dry-run")...)
	var preview tags.BatchChange
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.ChangedTracks != 1 || preview.RemovedAssignments != 1 || len(preview.TrackIDs) != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	unchanged, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || len(unchanged.Values) != 1 || unchanged.Revision != before.Revision {
		t.Fatalf("preview changed stored assignments: %+v error=%v", unchanged, err)
	}
	output = executeCustomTagsJSON(t, append(args, "--plan-token", preview.PlanToken)...)
	var applied tags.BatchChange
	if err := json.Unmarshal([]byte(output), &applied); err != nil {
		t.Fatal(err)
	}
	stored, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || len(stored.Values) != 0 || stored.Revision != applied.Revision || !applied.Applied {
		t.Fatalf("batch apply=%+v stored=%+v error=%v", applied, stored, err)
	}
}

func executeCustomTagsJSON(t *testing.T, args ...string) string {
	t.Helper()
	var output, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatalf("custom-tags command: %v\n%s", err, diagnostics.String())
	}
	return output.String()
}

func TestCustomTagsBatchHumanReviewPreservesAssignments(t *testing.T) {
	library, d, before := customTagsCommandLibrary(t)
	output, err := executeCommand("--lib-dir", library, "track", "custom-tags", "batch-update", "--tracks", "1,1", "--revision", before.Revision, "--add=", "--remove", "warm", "--dry-run")
	if err != nil {
		t.Fatalf("human batch review: %v\n%s", err, output)
	}
	for _, detail := range []string{"Selected tracks: [1]\n", "Remove values: warm\n", "Changed tracks: 1\n", "Added assignments: 0\n", "Removed assignments: 1\n", "Applied: false\n", "Revision: " + before.Revision + "\n", "Plan token: "} {
		if !bytes.Contains([]byte(output), []byte(detail)) {
			t.Errorf("human review missing %q:\n%s", detail, output)
		}
	}
	stored, err := tags.InspectTrack(t.Context(), d, library, "1")
	if err != nil || stored.Revision != before.Revision || len(stored.Values) != 1 || stored.Values[0] != "warm" {
		t.Fatalf("human preview changed assignments: %+v error=%v", stored, err)
	}
	for _, name := range []string{"backups", "history"} {
		if _, err := os.Stat(filepath.Join(library, ".engine-cli", name)); !os.IsNotExist(err) {
			t.Fatalf("human preview created %s: %v", name, err)
		}
	}
}

func TestCustomTagsBatchCommandsReportClosedOutput(t *testing.T) {
	for _, operation := range []string{"batch-inspect", "batch-update"} {
		t.Run(operation, func(t *testing.T) {
			library, d, before := customTagsCommandLibrary(t)
			output, err := os.CreateTemp(t.TempDir(), "closed-output")
			if err != nil {
				t.Fatal(err)
			}
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
			args := []string{"--lib-dir", library, "track", "custom-tags", operation, "--tracks", "1"}
			if operation == "batch-update" {
				args = append(args, "--revision", before.Revision, "--add=", "--remove", "warm", "--dry-run")
			}
			var diagnostics bytes.Buffer
			command := newRootCommand()
			command.SetOut(output)
			command.SetErr(&diagnostics)
			command.SetArgs(args)
			if err := command.Execute(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("closed stdout reported success or unrelated failure: %v\n%s", err, diagnostics.String())
			}
			stored, err := tags.InspectTrack(t.Context(), d, library, "1")
			if err != nil || stored.Revision != before.Revision || len(stored.Values) != 1 {
				t.Fatalf("read-only output failure changed assignments: %+v error=%v", stored, err)
			}
		})
	}
}
