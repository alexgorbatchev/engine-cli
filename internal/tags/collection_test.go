package tags

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCollectionTagsResolveEveryCurrentOriginWithoutPruning(t *testing.T) {
	library, engine := tagLibrary(t)
	state := State{
		Categories:  []Category{{ID: "category", Name: "Mood", Values: []Value{{ID: "warm", Name: "Warm"}, {ID: "cool", Name: "Cool"}}}},
		Assignments: []Assignment{{OriginUUID: "library-a", OriginTrackID: 10, ValueID: "warm"}, {OriginUUID: "library-a", OriginTrackID: 10, ValueID: "cool"}, {OriginUUID: "missing", OriginTrackID: 20, ValueID: "warm"}},
	}
	stored, err := Replace(t.Context(), library, currentRevision(t, library), state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ListTracks(t.Context(), engine, library)
	if err != nil {
		t.Fatal(err)
	}
	want := []TrackValues{{TrackID: 1, Values: []string{"cool", "warm"}}}
	if got.Revision != stored.Revision || !reflect.DeepEqual(got.Tracks, want) || got.UnavailableTracks != 1 {
		t.Fatalf("snapshot: %+v tracks=%+v; want %+v", got, got.Tracks, want)
	}
	for _, value := range got.Categories[0].Values {
		if value.TrackCount != 1 {
			t.Fatalf("current-origin assignment count mismatch: %+v", value)
		}
	}
	after, err := Read(t.Context(), library)
	if err != nil || after.Revision() != stored.Revision || len(after.Assignments) != 3 {
		t.Fatalf("read pruned unavailable assignments: %+v %v", after, err)
	}
	if _, err := engine.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	failed, err := ListTracks(t.Context(), engine, library)
	if err == nil || failed != nil {
		t.Fatalf("failed identity read returned partial snapshot: %+v %v", failed, err)
	}
}

func TestCollectionTagsRejectUnavailableObservationWithoutPartialData(t *testing.T) {
	library, engine := tagLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := ListTracks(ctx, engine, library)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled observation: %+v %v", got, err)
	}
	if _, err := engine.Exec("UPDATE Track SET originTrackId='invalid' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	got, err = ListTracks(t.Context(), engine, library)
	if err == nil || got != nil {
		t.Fatalf("invalid native identity returned partial snapshot: %+v %v", got, err)
	}
}

func TestCollectionTagsRejectLateNativeReadFailure(t *testing.T) {
	library, engine := tagLibrary(t)
	if _, err := engine.Exec("ALTER TABLE Track RENAME TO StoredTrack"); err != nil {
		t.Fatal(err)
	}
	// SQLite evaluates this expression while advancing the second row, after a
	// valid first identity has already been read.
	if _, err := engine.Exec(`CREATE VIEW Track AS SELECT id,originDatabaseUuid,CASE WHEN id=2 THEN json_extract('invalid-json','$') ELSE originTrackId END AS originTrackId FROM StoredTrack`); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.Query("SELECT id,originDatabaseUuid,originTrackId FROM Track ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() || rows.Next() || rows.Err() == nil {
		t.Fatal("fixture must yield one native row before its iteration error")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ListTracks(t.Context(), engine, library)
	if err == nil || got != nil {
		t.Fatalf("late native read returned a partial snapshot: %+v %v", got, err)
	}
}
