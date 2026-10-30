package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
	"github.com/spf13/cobra"
)

func TestCustomTagCollectionCommandReadAndJSON(t *testing.T) {
	library := createTestLibrary(t)
	args := []string{"--lib-dir", library, "track", "custom-tags", "list"}
	human, err := executeCommand(args...)
	if err != nil || !strings.Contains(human, "Assigned tracks: 0") || !strings.Contains(human, "Custom tags: 0 categories") {
		t.Fatalf("human snapshot: %q %v", human, err)
	}
	var output, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(append(args, "--json"))
	if err := command.Execute(); err != nil {
		t.Fatalf("JSON snapshot: %v %s", err, diagnostics.String())
	}
	var snapshot tags.CollectionTags
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision == "" || snapshot.Tracks == nil || len(snapshot.Tracks) != 0 || snapshot.Categories == nil {
		t.Fatalf("empty snapshot lost its collection contract: %+v", snapshot)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("read enrolled tags: %v", err)
	}
}

func assignedCollectionLibrary(t *testing.T) string {
	t.Helper()
	library := createTestLibrary(t)
	database, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("UPDATE Track SET originDatabaseUuid='fixture',originTrackId=1 WHERE id=1"); err != nil {
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
	return library
}

func TestCustomTagCollectionCommandPrintsAssignedTracks(t *testing.T) {
	library := assignedCollectionLibrary(t)
	args := []string{"--lib-dir", library, "track", "custom-tags", "list"}
	human, err := executeCommand(args...)
	if err != nil || !strings.Contains(human, "Assigned tracks: 1") || !strings.Contains(human, "Track 1: warm\n") {
		t.Fatalf("assigned human snapshot: %q %v", human, err)
	}
	var output, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetArgs(append(args, "--json"))
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var snapshot tags.CollectionTags
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tracks) != 1 || snapshot.Tracks[0].TrackID != 1 || len(snapshot.Tracks[0].Values) != 1 || snapshot.Tracks[0].Values[0] != "warm" {
		t.Fatalf("assigned JSON snapshot: %+v", snapshot)
	}
}

type collectionFailWriter struct {
	writes int
	failAt int
}

func (writer *collectionFailWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.writes == writer.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(data), nil
}

func TestCustomTagCollectionOutputFailureIsReturned(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		command := &cobra.Command{}
		command.SetOut(&collectionFailWriter{failAt: failAt})
		result := &tags.CollectionTags{Taxonomy: &tags.Taxonomy{Revision: "revision"}, Tracks: []tags.TrackValues{{TrackID: 1, Values: []string{"warm"}}}}
		handled, err := printCustomTags(command, result)
		if !handled || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write %d failure: handled=%t err=%v", failAt, handled, err)
		}
	}
}
