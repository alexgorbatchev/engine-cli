package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagsNativeCommandClearRequiresExplicitValues(t *testing.T) {
	library := createTestLibrary(t)
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
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
