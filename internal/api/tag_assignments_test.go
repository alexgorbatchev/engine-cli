package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

func TestCustomTagCollectionSnapshotDoesNotEnrollStorage(t *testing.T) {
	s, library := testAPI(t)
	job := runAPIJob(t, s, "track.custom-tags.list", `{}`)
	result, ok := job["result"].(map[string]any)
	if !ok || result["revision"] == "" {
		t.Fatalf("missing assignment snapshot: %+v", job)
	}
	for _, key := range []string{"categories", "tracks"} {
		if items, ok := result[key].([]any); !ok || len(items) != 0 {
			t.Fatalf("empty %s: %+v", key, result[key])
		}
	}
	if job["history_id"] != nil {
		t.Fatalf("read created history: %+v", job)
	}
	if _, err := os.Stat(companion.TagsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("snapshot enrolled storage: %v", err)
	}
}

func TestCustomTagCollectionSnapshotFailureHasNoPartialResult(t *testing.T) {
	s, library := testAPI(t)
	if err := os.MkdirAll(library+"/.engine-cli", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(companion.TagsPath(library), []byte("private corrupt storage diagnostic"), 0600); err != nil {
		t.Fatal(err)
	}
	job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.custom-tags.list", `{}`))
	if job["state"] != "failed" || job["result"] != nil {
		t.Fatalf("failed snapshot returned data: %+v", job)
	}
	if failure, ok := job["error"].(map[string]any); !ok || failure["code"] != "tag_store_unavailable" {
		t.Fatalf("unsafe or missing failure code: %+v", job)
	}
}

func TestCustomTagCollectionSnapshotIncludesCurrentAssignments(t *testing.T) {
	s, library := testAPI(t)
	engine, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Exec("UPDATE Track SET originDatabaseUuid='snapshot-library',originTrackId=id WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	state, err := tags.Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := tags.Replace(t.Context(), library, state.Revision(), tags.State{
		Categories:  []tags.Category{{ID: "category", Name: "Mood", Values: []tags.Value{{ID: "warm", Name: "Warm"}}}},
		Assignments: []tags.Assignment{{OriginUUID: "snapshot-library", OriginTrackID: 1, ValueID: "warm"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.custom-tags.list", `{}`)
	result, ok := job["result"].(map[string]any)
	if !ok || result["revision"] != stored.Revision {
		t.Fatalf("inconsistent revision: %+v", job)
	}
	categories, ok := result["categories"].([]any)
	if !ok || len(categories) != 1 || categories[0].(map[string]any)["name"] != "Mood" {
		t.Fatalf("missing taxonomy: %+v", result)
	}
	tracks, ok := result["tracks"].([]any)
	if !ok || len(tracks) != 1 || tracks[0].(map[string]any)["track_id"] != float64(1) {
		t.Fatalf("missing current assignment: %+v", result)
	}
}
