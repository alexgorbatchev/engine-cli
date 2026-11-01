package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func executeHandoffCommand(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "command-output-")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	defer func() {
		os.Stdout = previous
		if err := output.Close(); err != nil {
			t.Errorf("close command output: %v", err)
		}
	}()
	cmd := command()
	cmd.SetArgs(args)
	cmd.SetOut(output)
	cmd.SetErr(output)
	runErr := cmd.ExecuteContext(t.Context())
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data, runErr
}

func TestHandoffCommandSeedsVerifiesAndRefusesWithoutClobbering(t *testing.T) {
	in := committedNativeInputs(t)
	inputPath := filepath.Join(t.TempDir(), "inputs.json")
	if err := writeJSON(inputPath, in); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "fixtures")
	args := []string{"--root", root, "--inputs", inputPath}
	if output, err := executeHandoffCommand(t, append(args, "seed")...); err != nil {
		t.Fatalf("seed command: %v %s", err, output)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "healthy"} {
		t.Run("verify-"+name, func(t *testing.T) {
			output, err := executeHandoffCommand(t, append(args, "verify", "--name", name, "--expect", "seed")...)
			if err != nil {
				t.Fatalf("verify command: %v %s", err, output)
			}
			var result struct {
				Verified bool   `json:"verified"`
				Expected string `json:"expected"`
				Name     string `json:"name"`
				Desktop  string `json:"desktop_evaluation"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("verify success output: %v %s", err, output)
			}
			if !result.Verified || result.Expected != "seed" || result.Name != name || result.Desktop != "NOT_PROVEN" {
				t.Fatalf("verification result does not identify actual requested scope: %+v", result)
			}
		})
	}
	for _, tt := range []struct {
		name  string
		args  []string
		exist bool
	}{
		{"existing-seed", []string{"seed"}, true},
		{"unknown-case", []string{"verify", "--name", "not-a-fixture"}, false},
		{"unknown-state", []string{"verify", "--expect", "not-a-state"}, false},
		{"unperformed-repair", []string{"verify", "--name", "reconnect", "--expect", "repaired"}, false},
		{"unexpected-seed-argument", []string{"seed", "extra"}, false},
		{"unexpected-verify-argument", []string{"verify", "extra"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output, err := executeHandoffCommand(t, append(args, tt.args...)...)
			if err == nil || len(output) != 0 {
				t.Fatalf("refused command published success: err=%v output=%s", err, output)
			}
			if tt.exist && !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing root refusal lost native cause: %v", err)
			}
			after, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("refused command changed existing seed manifest")
			}
		})
	}
	if output, err := executeHandoffCommand(t, append(args, "verify")...); err != nil {
		t.Fatalf("all-case recovery verification: %v %s", err, output)
	}
	t.Run("absolute-path-boundaries", func(t *testing.T) {
		localInput := filepath.Join(root, "command-inputs.json")
		if err := writeJSON(localInput, in); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		for _, tt := range []struct {
			name, root, inputs string
		}{
			{"relative-root", ".", localInput},
			{"empty-root", "", localInput},
			{"relative-inputs", root, filepath.Base(localInput)},
		} {
			t.Run(tt.name, func(t *testing.T) {
				output, err := executeHandoffCommand(t, "verify", "--root", tt.root, "--inputs", tt.inputs, "--name", "healthy")
				if err == nil || len(output) != 0 {
					t.Fatalf("nonabsolute path accepted against valid native fixture: err=%v output=%s", err, output)
				}
			})
		}
		if output, err := executeHandoffCommand(t, "verify", "--root", root, "--inputs", localInput); err != nil {
			t.Fatalf("absolute-path recovery verification: %v %s", err, output)
		}
	})
}

func TestHandoffCommandRejectsInvalidInputsBeforeCreatingRoot(t *testing.T) {
	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"seed", "verify"} {
		for _, tt := range []struct {
			name string
			args []string
		}{
			{"missing-inputs", nil},
			{"absent-inputs", []string{"--inputs", filepath.Join(t.TempDir(), "absent.json")}},
			{"malformed-inputs", []string{"--inputs", malformed}},
		} {
			t.Run(operation+"/"+tt.name, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "must-not-exist")
				args := append([]string{operation, "--root", root}, tt.args...)
				output, err := executeHandoffCommand(t, args...)
				if err == nil || len(output) != 0 {
					t.Fatalf("invalid inputs accepted: err=%v output=%s", err, output)
				}
				if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid input created output root: %v", err)
				}
			})
		}
	}
}
