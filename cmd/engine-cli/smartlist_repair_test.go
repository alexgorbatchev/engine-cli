package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func executeSmartlistRepairCLI(args ...string) (string, error) {
	var out, diagnostics bytes.Buffer
	command := newRootCommand()
	command.SetOut(&out)
	command.SetErr(&diagnostics)
	command.SetArgs(args)
	err := command.Execute()
	return out.String(), err
}

func smartlistRepairCLILibrary(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{
			systemcheck.EngineDesktop: {Status: "ok", Running: new(false)},
		}}, nil
	}
	lib := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('broken','Saved','Missing;','','','opaque')`); err != nil {
		t.Fatal(err)
	}
	return lib
}

func smartlistRepairCLIReview(t *testing.T, lib string) history.SmartlistRepairReview {
	t.Helper()
	out, err := executeSmartlistRepairCLI("-l", lib, "library", "smartlist-repair", "preview", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var review history.SmartlistRepairReview
	if err := json.Unmarshal([]byte(out), &review); err != nil {
		t.Fatalf("decode actual CLI review: %v\n%s", err, out)
	}
	if review.PlanToken == "" || review.Removed != 1 || len(review.Rows) != 1 || review.Rows[0].Name != "Saved" {
		t.Fatalf("actual repair review: %+v", review)
	}
	return review
}

func TestSmartlistRepairCLIReviewApplyRecover(t *testing.T) {
	for _, mode := range []string{"human", "agent", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			}
			lib := smartlistRepairCLILibrary(t)
			review := smartlistRepairCLIReview(t, lib)
			if mode != "json" {
				out, err := executeSmartlistRepairCLI("-l", lib, "library", "smartlist-repair", "preview")
				if err != nil || !strings.Contains(out, review.PlanToken) || !strings.Contains(out, `remove: "Saved"`) || json.Valid([]byte(out)) {
					t.Fatalf("named readable preview: error=%v output=%s", err, out)
				}
			}
			if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("read-only preview enrolled library storage: %v", err)
			}
			args := []string{"-l", lib, "library", "smartlist-repair", "apply", "--plan-token", review.PlanToken}
			if mode == "json" {
				args = append(args, "--json")
			}
			out, err := executeSmartlistRepairCLI(args...)
			if err != nil {
				t.Fatalf("apply reviewed CLI repair: %v\n%s", err, out)
			}
			switch mode {
			case "json":
				var result history.SmartlistRepairResult
				if err := json.Unmarshal([]byte(out), &result); err != nil || !result.Applied || result.Removed != 1 || result.UndoID != "" {
					t.Fatalf("CLI repair result: %+v error=%v output=%s", result, err, out)
				}
			case "human":
				if !strings.Contains(out, "Applied: true") || !strings.Contains(out, `remove: "Saved"`) {
					t.Fatalf("missing named human repair result: %s", out)
				}
			case "agent":
				if !strings.Contains(out, "applied: true\nreconnected: 0\nremoved: 1") || !strings.Contains(out, `remove: "Saved" uuid=broken`) {
					t.Fatalf("missing compact named agent repair result: %s", out)
				}
			}
			database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var count int
			if err := database.QueryRow(`SELECT count(*) FROM Smartlist`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("CLI did not actually remove damaged native row: count=%d error=%v", count, err)
			}
			entries, err := history.List(lib)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unknown orphan removal invented Undo: entries=%d error=%v", len(entries), err)
			}
			out, err = executeSmartlistRepairCLI("-l", lib, "library", "smartlist-repair", "recover", "--json")
			var recovery history.SmartlistRepairRecovery
			if err != nil {
				t.Fatalf("recover completed repair: %v", err)
			}
			if err := json.Unmarshal([]byte(out), &recovery); err != nil || recovery.RolledBack || recovery.RestoredTargets != 0 {
				t.Fatalf("completed repair incorrectly recovered: %+v error=%v", recovery, err)
			}
			if mode != "json" {
				out, err = executeSmartlistRepairCLI("-l", lib, "library", "smartlist-repair", "recover")
				want := "Rolled back interrupted repair: false"
				if mode == "agent" {
					want = "rolled_back: false\nrestored_targets: 0"
				}
				if err != nil || !strings.Contains(out, want) {
					t.Fatalf("readable recovery: error=%v output=%s", err, out)
				}
			}
		})
	}
}

func TestSmartlistRepairCLIReportsClosedOutput(t *testing.T) {
	lib := smartlistRepairCLILibrary(t)
	file, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	command.SetOut(file)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"-l", lib, "library", "smartlist-repair", "preview"})
	if err := command.Execute(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("lost native output error: %v", err)
	}
}

func TestSmartlistRepairCLIPendingUndoRefusesBeforeBackup(t *testing.T) {
	lib := smartlistRepairCLILibrary(t)
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(active, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "pending.json"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := executeSmartlistRepairCLI("-l", lib, "library", "undo")
	if progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("pending native Undo: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli", "backups")); !os.IsNotExist(err) {
		t.Fatalf("pending native Undo took a backup: %v", err)
	}
}

func TestSmartlistRepairCLIRejectsUnreviewedChanges(t *testing.T) {
	for _, name := range []string{"missing-token", "stale-token", "extra-argument"} {
		t.Run(name, func(t *testing.T) {
			lib := smartlistRepairCLILibrary(t)
			review := smartlistRepairCLIReview(t, lib)
			args := []string{"-l", lib, "library", "smartlist-repair", "apply"}
			switch name {
			case "stale-token":
				args = append(args, "--plan-token", "stale-"+review.PlanToken)
			case "extra-argument":
				args = append(args, "unexpected", "--plan-token", review.PlanToken)
			}
			if out, err := executeCommand(args...); err == nil {
				t.Fatalf("unreviewed CLI repair succeeded: %s", out)
			}
			current := smartlistRepairCLIReview(t, lib)
			if current.PlanToken != review.PlanToken {
				t.Fatal("rejected CLI request changed the observed collection/history")
			}
		})
	}
}
