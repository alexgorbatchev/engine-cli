package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputsRefuseUnavailableOrChangedEvidenceBeforeSeed(t *testing.T) {
	in := committedNativeInputs(t)
	valid := filepath.Join(t.TempDir(), "inputs.json")
	if err := writeJSON(valid, in); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadInputs(t.Context(), valid, filepath.Join(t.TempDir(), "not-seeded")); err != nil {
		t.Fatalf("intact native evidence rejected: %v", err)
	}
	for _, name := range []string{"trailing-json", "short-commit", "relative-source", "missing-source", "foreign-head", "missing-binary", "changed-binary", "relative-binding", "short-digest", "changed-golden", "malformed-build", "malformed-approval", "not-native-binary"} {
		t.Run(name, func(t *testing.T) {
			changed := in
			dir := t.TempDir()
			boundFile := func(name string, data []byte) binding {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := writeNew(path, data); err != nil {
					t.Fatal(err)
				}
				return binding{path, digest(data)}
			}
			switch name {
			case "short-commit":
				changed.SourceCommit = "abc"
			case "relative-source":
				changed.SourceRoot = "."
			case "missing-source":
				changed.SourceRoot = filepath.Join(dir, "absent")
			case "foreign-head":
				changed.SourceCommit = strings.Repeat("0", 40)
			case "missing-binary":
				changed.Binary.Path = filepath.Join(dir, "absent")
			case "changed-binary":
				changed.Binary.SHA256 = strings.Repeat("0", 64)
			case "relative-binding":
				changed.Binary.Path = "engine-cli"
			case "short-digest":
				changed.Binary.SHA256 = "abc"
			case "changed-golden":
				changed.Golden = boundFile("golden.json", []byte("{}\n"))
			case "malformed-build":
				changed.BuildQualification = boundFile("build.json", []byte("{"))
			case "malformed-approval":
				changed.ExecutionApproval = boundFile("approval.json", []byte("{"))
			case "not-native-binary":
				changed.Binary = boundFile("engine-cli", []byte("not a native executable"))
				build := map[string]any{}
				data, err := readBound(in.BuildQualification)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &build); err != nil {
					t.Fatal(err)
				}
				build["binary_sha256"] = changed.Binary.SHA256
				data, err = json.Marshal(build)
				if err != nil {
					t.Fatal(err)
				}
				changed.BuildQualification = boundFile("build.json", data)
				approval := map[string]any{}
				data, err = readBound(in.ExecutionApproval)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &approval); err != nil {
					t.Fatal(err)
				}
				approval["binary_sha256"], approval["build_sha256"] = changed.Binary.SHA256, changed.BuildQualification.SHA256
				data, err = json.Marshal(approval)
				if err != nil {
					t.Fatal(err)
				}
				changed.ExecutionApproval = boundFile("approval.json", data)
			}
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if name == "trailing-json" {
				data = append(data, []byte(" {}")...)
			}
			path := filepath.Join(dir, "inputs.json")
			if err := writeNew(path, data); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dir, "not-seeded")
			if _, _, err := loadInputs(t.Context(), path, root); err == nil {
				t.Fatal("damaged build evidence accepted")
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal created fixture root: %v", err)
			}
		})
	}
}
