package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/fixturecapture"
)

func TestLibraryFixtureCapturePublicCommand(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			base := t.TempDir()
			before := filepath.Join(base, "before")
			after := filepath.Join(base, "after")
			for _, path := range []string{before, after} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(path, "m.db")))
				if err != nil {
					t.Fatal(err)
				}
				_, execErr := db.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, rules TEXT); INSERT INTO Smartlist VALUES ('synthetic','{}')")
				closeErr := db.Close()
				if execErr != nil {
					t.Fatal(execErr)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			out := filepath.Join(base, "capture")
			selected := filepath.Join(base, "unopened-default-library")
			command := newRootCommand()
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			args := []string{"--lib-dir", selected, "library", "fixture", "capture", "--before", before, "--after", after, "--out-dir", out}
			if asJSON {
				args = append(args, "--json")
			}
			command.SetArgs(args)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if asJSON {
				var report fixturecapture.Report
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatalf("non-JSON stdout: %s: %v", stdout.String(), err)
				}
				if len(report.Before.Files) != 1 || !strings.Contains(report.Privacy, "private") {
					t.Fatalf("missing actual inventory/privacy: %#v", report)
				}
			} else if !strings.Contains(stdout.String(), "Review privacy") {
				t.Fatal(stdout.String())
			}
			if _, err := os.Stat(selected); !os.IsNotExist(err) {
				t.Fatalf("default library was enrolled: %v", err)
			}
			for _, path := range []string{before, after} {
				if _, err := os.Stat(filepath.Join(path, ".engine-cli")); !os.IsNotExist(err) {
					t.Fatalf("input enrolled: %v", err)
				}
			}
			command = newRootCommand()
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			command.SetArgs(args)
			if err := command.ExecuteContext(t.Context()); !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing capture clobber accepted: %v", err)
			}
		})
	}
}

func TestLibraryFixtureCaptureRequiresExplicitInputs(t *testing.T) {
	command := newRootCommand()
	command.SetArgs([]string{"library", "fixture", "capture"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "required flag") {
		t.Fatalf("missing explicit input accepted: %v", err)
	}
}
