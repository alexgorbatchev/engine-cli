package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputsRefuseUnavailableAndDamagedCommittedGolden(t *testing.T) {
	in := committedNativeInputs(t)
	root := filepath.Join(t.TempDir(), "not-seeded")
	load := func(in inputs) error {
		t.Helper()
		path := filepath.Join(t.TempDir(), "inputs.json")
		if err := writeJSON(path, in); err != nil {
			t.Fatal(err)
		}
		_, _, err := loadInputs(t.Context(), path, root)
		return err
	}
	if err := load(in); err != nil {
		t.Fatal(err)
	}
	t.Run("unavailable-revision", func(t *testing.T) {
		changed := in
		changed.GoldenCommit = strings.Repeat("0", 40)
		if err := load(changed); err == nil || !strings.Contains(err.Error(), "merge-base") {
			t.Fatalf("unavailable golden revision not refused: %v", err)
		}
	})
	t.Run("missing-bound-file", func(t *testing.T) {
		changed := in
		changed.Golden.Path = filepath.Join(t.TempDir(), "absent.json")
		if err := load(changed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing bound golden not refused: %v", err)
		}
	})
	for _, tt := range []struct {
		name, data string
		syntax     bool
	}{
		{"malformed-committed-golden", "{", true},
		{"incomplete-committed-golden", "{}", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := git(context.WithoutCancel(t.Context()), in.SourceRoot, "reset", "--hard", in.SourceCommit); err != nil {
					t.Error(err)
				}
			})
			if err := os.WriteFile(in.Golden.Path, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", goldenPath}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", tt.name}} {
				if _, err := git(t.Context(), in.SourceRoot, args...); err != nil {
					t.Fatal(err)
				}
			}
			head, err := git(t.Context(), in.SourceRoot, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			changed := in
			changed.SourceCommit = strings.TrimSpace(string(head))
			changed.GoldenCommit = changed.SourceCommit
			changed.Golden.SHA256 = digest([]byte(tt.data))
			err = load(changed)
			var syntax *json.SyntaxError
			if tt.syntax {
				if !errors.As(err, &syntax) {
					t.Fatalf("malformed committed JSON not refused at decode: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "authentic title records") {
				t.Fatalf("incomplete committed golden not refused at validation: %v", err)
			}
		})
	}
	if err := load(in); err != nil {
		t.Fatalf("restored committed golden refused: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provenance refusal created fixture output: %v", err)
	}
}

func TestBuildRefusesActualCLIWithoutReviewedSourceMetadata(t *testing.T) {
	in := committedNativeInputs(t)
	if err := validateBuild(in); err != nil {
		t.Fatalf("intact native build refused: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "engine-cli")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-p=1", "-buildvcs=false", "-o", binary, "./cmd/engine-cli")
	cmd.Dir = in.SourceRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building actual CLI without VCS metadata: %v %s", err, output)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	in.Binary = binding{Path: binary, SHA256: digest(data)}
	rebind := func(old binding, fields map[string]any) binding {
		t.Helper()
		data, err := readBound(old)
		if err != nil {
			t.Fatal(err)
		}
		var receipt map[string]any
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		for key, value := range fields {
			receipt[key] = value
		}
		path := filepath.Join(t.TempDir(), "receipt.json")
		if err := writeJSON(path, receipt); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return binding{Path: path, SHA256: digest(data)}
	}
	in.BuildQualification = rebind(in.BuildQualification, map[string]any{"binary_sha256": in.Binary.SHA256})
	in.ExecutionApproval = rebind(in.ExecutionApproval, map[string]any{"binary_sha256": in.Binary.SHA256, "build_sha256": in.BuildQualification.SHA256})
	if err := validateBuild(in); err == nil || !strings.Contains(err.Error(), "binary metadata") {
		t.Fatalf("hash-bound real CLI without reviewed source metadata accepted: %v", err)
	}
}
