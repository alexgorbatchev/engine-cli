package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
)

func TestLibraryPreferencesCLIReopensState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "Library")
	statePath := filepath.Join(t.TempDir(), "state.db")
	for _, action := range [][]string{
		{"update", path, "--name", "Evening sets", "--hidden=true"},
		{"list"},
		{"update", path, "--hidden=false"},
	} {
		root := newRootCommand()
		var output, diagnostics bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&diagnostics)
		root.SetArgs(append(append([]string{"library", "preferences"}, action...), "--state-db", statePath, "--json"))
		if err := root.Execute(); err != nil {
			t.Fatalf("preferences: %v %s", err, output.String())
		}
		var libraries []api.KnownLibrary
		if err := json.Unmarshal(output.Bytes(), &libraries); err != nil {
			t.Fatal(err)
		}
		wantHidden := action[len(action)-1] != "--hidden=false"
		// The remembered folder holds no library, so its identity cannot be read.
		if len(libraries) != 1 || libraries[0].Name != "Evening sets" || libraries[0].Hidden != wantHidden || libraries[0].Path != "~/Library" || libraries[0].Registration != libraryops.RegistrationUnavailable {
			t.Fatalf("preferences result: %+v", libraries)
		}
	}
}

func TestLifecycleCLIRequiresReviewedPlan(t *testing.T) {
	path := createTestLibrary(t)
	for _, action := range [][]string{{"delete", path}, {"move", path, "--destination", filepath.Join(t.TempDir(), "moved")}, {"media", "aggregate", path, "--mode", "copy"}} {
		root := newRootCommand()
		var output, diagnostics bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&diagnostics)
		root.SetArgs(append(append([]string{"library"}, action...), "--dry-run", "--json"))
		if err := root.Execute(); err != nil {
			t.Fatalf("preview: %v %s", err, output.String())
		}
		var plan libraryops.Plan
		if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		if plan.PlanToken == "" || plan.Path != displayPath(path) {
			t.Fatalf("invalid preview: %+v", plan)
		}
		root = newRootCommand()
		root.SetOut(&output)
		root.SetErr(&diagnostics)
		root.SetArgs(append(append([]string{"library"}, action...), "--json"))
		if err := root.Execute(); err == nil {
			t.Fatal("unreviewed file change accepted")
		}
	}
}

func TestLibraryPlanDisplayAbbreviatesEveryHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	peer := libraryops.Library{Path: filepath.Join(home, "Peer"), Name: "Peer"}
	offline := libraryops.Library{Path: filepath.Join(home, "Offline")}
	plan := &libraryops.Plan{
		Path:        filepath.Join(home, "Library"),
		Destination: filepath.Join(home, "Moved"),
		Files: []libraryops.File{{
			Path:        filepath.Join(home, "Library", "song.mp3"),
			Destination: filepath.Join(home, "Moved", "song.mp3"),
			Libraries:   []libraryops.Library{peer},
		}},
		AffectedLibraries:    []libraryops.Library{peer},
		UnavailableLibraries: []libraryops.Library{offline},
	}
	display := displayLibraryPlan(plan)
	file := display.Files[0]
	for _, path := range []string{display.Path, display.Destination, file.Path, file.Destination, file.Libraries[0].Path, display.AffectedLibraries[0].Path, display.UnavailableLibraries[0].Path} {
		if !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
			t.Fatalf("home path not abbreviated: %q in %+v", path, display)
		}
	}
	// The reviewed plan keeps canonical paths; only its displayed copy changes.
	if plan.Files[0].Libraries[0].Path != peer.Path || plan.AffectedLibraries[0].Path != peer.Path || plan.UnavailableLibraries[0].Path != offline.Path {
		t.Fatalf("display rewrote the reviewed plan: %+v", plan)
	}
}
