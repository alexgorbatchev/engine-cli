package api

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db/dbtest"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestAPIWatchMoveAndUndoRetainLiveAuthorization(t *testing.T) {
	s, lib := testAPI(t)
	owner, before := configureAPIWatch(t, s, lib)
	destination := filepath.Join(t.TempDir(), "moved")
	opts := libraryops.Options{Path: lib, Destination: destination, DryRun: true}
	preview, _, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, preview.(*libraryops.Plan).PlanToken
	_, historyID, err := s.RunOperation(t.Context(), "library.move", mustJSON(t, opts))
	if err != nil || historyID == "" {
		t.Fatalf("move: history=%q error=%v", historyID, err)
	}
	if _, err := owner.WatchList(t.Context()); progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("source watcher survived move: %v", err)
	}
	moved, err := s.intake.open(destination, false)
	if err != nil || moved == nil {
		t.Fatalf("destination watcher did not reopen: %v", err)
	}
	assertAPIWatchAuthorization(t, moved, before)
	// A consent token obtained before relocation still identifies the same live
	// authority. Operational changes after the move must survive its Undo.
	disabled, err := moved.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: before.Roots[0].ID, Revision: before.Revision}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunOperation(t.Context(), "library.undo", mustJSON(t, undoParams{Library: destination, ID: historyID})); err != nil {
		t.Fatal(err)
	}
	if _, err := moved.WatchList(t.Context()); progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("destination watcher survived Undo relocation: %v", err)
	}
	restored, err := s.intake.open(lib, false)
	if err != nil || restored == nil {
		t.Fatalf("source watcher did not resume after Undo: %v", err)
	}
	assertAPIWatchAuthorization(t, restored, disabled)
	if _, err := restored.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: disabled.Roots[0].ID, Revision: disabled.Revision}, true); err != nil {
		t.Fatalf("live authority was lost during Undo: %v", err)
	}
}

func TestAPIWatchSyncKeepsDestinationAuthority(t *testing.T) {
	s, _ := testAPI(t)
	source, destination := dbtest.Library(t, "source"), dbtest.Library(t, "destination")
	sourceOwner, sourceState := configureAPIWatch(t, s, source)
	destinationOwner, destinationState := configureAPIWatch(t, s, destination)
	params := map[string]any{"source": source, "destination": destination, "scope": map[string]bool{"root": true}, "dry_run": true}
	preview, _, err := s.RunOperation(t.Context(), "library.sync", mustJSON(t, params))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		PlanToken string `json:"plan_token"`
	}
	if err := json.Unmarshal(mustJSON(t, preview), &plan); err != nil {
		t.Fatal(err)
	}
	params["dry_run"], params["plan_token"] = false, plan.PlanToken
	if _, _, err := s.RunOperation(t.Context(), "library.sync", mustJSON(t, params)); err != nil {
		t.Fatal(err)
	}
	assertAPIWatchAuthorization(t, sourceOwner, sourceState)
	if _, err := destinationOwner.WatchList(t.Context()); progress.ErrorCode(err) != "intake_closed" {
		t.Fatalf("destination watcher was not joined before sync: %v", err)
	}
	current, err := s.intake.open(destination, false)
	if err != nil || current == nil {
		t.Fatalf("destination watcher did not resume: %v", err)
	}
	assertAPIWatchAuthorization(t, current, destinationState)
	if _, err := current.WatchSetEnabled(t.Context(), intake.WatchRootOptions{ID: destinationState.Roots[0].ID, Revision: destinationState.Revision}, false); err != nil {
		t.Fatalf("sync replaced destination authority with foreign state: %v", err)
	}
}

func configureAPIWatch(t *testing.T, s *Server, lib string) (*intake.Service, intake.WatchState) {
	t.Helper()
	owner, err := s.intake.open(lib, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	initial, err := owner.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := owner.WatchAdd(t.Context(), intake.WatchAddOptions{Path: t.TempDir(), Recursive: true, Revision: initial.Revision})
	if err != nil || len(state.Roots) != 1 {
		t.Fatalf("configure watcher: state=%+v error=%v", state, err)
	}
	return owner, state
}

func assertAPIWatchAuthorization(t *testing.T, owner *intake.Service, want intake.WatchState) {
	t.Helper()
	got, err := owner.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != want.Revision || got.Suspended != want.Suspended || got.Reason != want.Reason || len(got.Roots) != 1 || got.Roots[0].ID != want.Roots[0].ID || got.Roots[0].Path != want.Roots[0].Path || got.Roots[0].Enabled != want.Roots[0].Enabled || got.Roots[0].Recursive != want.Roots[0].Recursive {
		t.Fatalf("library operation changed live watcher authorization: got=%+v want=%+v", got, want)
	}
}
