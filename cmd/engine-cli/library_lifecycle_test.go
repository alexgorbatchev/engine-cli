package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/state"
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
		var libraries []state.Library
		if err := json.Unmarshal(output.Bytes(), &libraries); err != nil {
			t.Fatal(err)
		}
		wantHidden := action[len(action)-1] != "--hidden=false"
		if len(libraries) != 1 || libraries[0].Name != "Evening sets" || libraries[0].Hidden != wantHidden || libraries[0].Path != "~/Library" {
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
