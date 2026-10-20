package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI unit tests use a deterministic OS observation, independent of the user's desktop.
func setupSystemTests() {
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false), PIDs: []int{}}}}, nil
	}
}

func TestCheckFailuresAndOutput(t *testing.T) {
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	if _, err := executeCommand("system", "check", "--checks", "unknown"); err == nil {
		t.Fatal("accepted unknown check")
	}
	for _, running := range []*bool{nil, new(false)} {
		querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
			return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: running}}}, nil
		}
		if out, err := executeCommand("system", "check"); err != nil || !strings.Contains(out, "running:") {
			t.Fatalf("%s %v", out, err)
		}
	}
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) { return nil, errors.New("unavailable") }
	for _, args := range [][]string{{"system", "check"}, {"playlist", "create", "Blocked"}} {
		if _, err := executeCommand(args...); err == nil {
			t.Fatal("ignored query error")
		}
	}
	// Dry-run bypasses the mutation guard and reaches normal argument validation.
	_, err := executeCommand("library", "sync", "--root", "--dry-run")
	if err == nil || strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("preview err=%v", err)
	}
	querySystem = original
	for _, json := range []bool{false, true} {
		c := newRootCommand()
		c.SetOut(upgradeFailWriter{})
		c.SetErr(&bytes.Buffer{})
		args := []string{"system", "check"}
		if json {
			args = append(args, "--json")
		}
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("ignored output error")
		}
	}
}

func TestSystemCheckAndMutationGuard(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		out, err := executeCommand("system", "check", "--json")
		var r systemcheck.Report
		if err != nil || json.Unmarshal([]byte(out), &r) != nil || *r.Checks[systemcheck.EngineDesktop].Running {
			t.Fatalf("%s %v", out, err)
		}
	}
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	lib := createTestLibrary(t)
	_, err := executeCommand("-l", lib, "playlist", "create", "Blocked")
	if err == nil || !strings.Contains(err.Error(), "close it") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("wrote history or backup: %v", err)
	}
	if _, err := executeCommand("-l", lib, "playlist", "list"); err != nil {
		t.Fatal(err)
	}
}
