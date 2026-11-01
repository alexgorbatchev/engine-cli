package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Build the actual committed application in an isolated clean source checkout.
// A tiny substitute program would not exercise the CLI package metadata contract.
func committedNativeInputs(t *testing.T) inputs {
	t.Helper()
	ctx := t.Context()
	project, err := git(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := git(ctx, strings.TrimSpace(string(project)), "archive", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsLocal(header.Name) {
			t.Fatal("non-local committed archive path")
		}
		path := filepath.Join(root, header.Name)
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			// Git records commit metadata in a PAX global header, not a source file.
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, os.FileMode(header.Mode)); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported committed archive entry %s", header.Name)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "actual committed CLI source"}} {
		if _, err := git(ctx, root, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(head))
	out := t.TempDir()
	binary := filepath.Join(out, "engine-cli")
	cmd := exec.CommandContext(ctx, "go", "build", "-p=1", "-o", binary, "./cmd/engine-cli")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual clean CLI build: %v %s", err, output)
	}
	bound := func(path string) binding {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return binding{Path: path, SHA256: digest(data)}
	}
	in := inputs{SourceRoot: root, SourceCommit: commit, GoldenCommit: commit, Binary: bound(binary), Golden: bound(filepath.Join(root, goldenPath))}
	buildPath := filepath.Join(out, "build.json")
	if err := writeJSON(buildPath, map[string]any{"source_commit": commit, "binary_sha256": in.Binary.SHA256, "goos": runtime.GOOS, "goarch": runtime.GOARCH, "clean": true}); err != nil {
		t.Fatal(err)
	}
	in.BuildQualification = bound(buildPath)
	approvalPath := filepath.Join(out, "approval.json")
	if err := writeJSON(approvalPath, map[string]any{"source_commit": commit, "binary_sha256": in.Binary.SHA256, "golden_sha256": in.Golden.SHA256, "build_sha256": in.BuildQualification.SHA256, "approved": true}); err != nil {
		t.Fatal(err)
	}
	in.ExecutionApproval = bound(approvalPath)
	return in
}

func TestInputsRequireActualCleanNativeBuildAndMatchingApproval(t *testing.T) {
	in := committedNativeInputs(t)
	inputPath := filepath.Join(t.TempDir(), "inputs.json")
	root := filepath.Join(t.TempDir(), "not-seeded")
	if err := writeJSON(inputPath, in); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadInputs(t.Context(), inputPath, root); err != nil {
		t.Fatalf("real clean native build rejected: %v", err)
	}
	for _, tt := range []struct {
		name, field string
		value       any
		approval    bool
	}{
		{"unclean-build", "clean", false, false},
		{"foreign-build-source", "source_commit", strings.Repeat("0", 40), false},
		{"foreign-build-binary", "binary_sha256", strings.Repeat("0", 64), false},
		{"foreign-build-os", "goos", "other", false},
		{"foreign-build-arch", "goarch", "other", false},
		{"unapproved-execution", "approved", false, true},
		{"foreign-approved-source", "source_commit", strings.Repeat("0", 40), true},
		{"foreign-approved-binary", "binary_sha256", strings.Repeat("0", 64), true},
		{"foreign-approved-golden", "golden_sha256", strings.Repeat("0", 64), true},
		{"foreign-approved-build", "build_sha256", strings.Repeat("0", 64), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := in
			selected := in.BuildQualification
			if tt.approval {
				selected = in.ExecutionApproval
			}
			data, err := readBound(selected)
			if err != nil {
				t.Fatal(err)
			}
			var receipt map[string]any
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			receipt[tt.field] = tt.value
			path := filepath.Join(t.TempDir(), "receipt.json")
			if err := writeJSON(path, receipt); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.approval {
				changed.ExecutionApproval = binding{path, digest(data)}
			} else {
				changed.BuildQualification = binding{path, digest(data)}
			}
			changedPath := filepath.Join(t.TempDir(), "inputs.json")
			if err := writeJSON(changedPath, changed); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadInputs(t.Context(), changedPath, root); err == nil {
				t.Fatal("hash-valid incompatible provenance accepted")
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("provenance refusal seeded output: %v", err)
			}
		})
	}
}

func TestInputsRefuseUntrackedBuildSourceBeforeSeed(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "synthetic source"}} {
		c := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if output, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, output)
		}
	}
	head, err := git(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	in := inputs{SourceRoot: root, SourceCommit: strings.TrimSpace(string(head)), GoldenCommit: strings.TrimSpace(string(head))}
	path := filepath.Join(t.TempDir(), "inputs.json")
	if err := writeJSON(path, in); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unreviewed.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(t.TempDir(), "not-created")
	_, _, err = loadInputs(t.Context(), path, fixtureRoot)
	if err == nil || !strings.Contains(err.Error(), "untracked") {
		t.Fatalf("untracked build source not refused: %v", err)
	}
	if _, err := os.Stat(fixtureRoot); !os.IsNotExist(err) {
		t.Fatalf("source refusal created root: %v", err)
	}
}
