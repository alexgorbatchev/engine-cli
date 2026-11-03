package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
)

func TestSharedDependencyCLIUsesAggregateOperation(t *testing.T) {
	previous := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = previous })
	for _, action := range []string{"inspect", "install", "update"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			acquisitionRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				if slices.Equal(args, []string{"--version"}) {
					return []byte("9.0.0"), nil
				}
				if slices.Equal(args, []string{"--json", "dependencies"}) {
					return []byte(`{"status":"ok","dependencies":[{"name":"shared","installed":true,"detectedVersion":"1.0","minVersion":"2.0","satisfied":false}],"changed":[]}`), nil
				}
				calls++
				if !slices.Equal(args, []string{"--json", "dependencies", "update", "shared"}) {
					t.Fatalf("dispatch %s %v", tool, args)
				}
				if tool == "fetch-track" {
					return nil, errors.New("installation unavailable")
				}
				return []byte(`{"status":"ok","dependencies":[],"changed":["shared"]}`), nil
			}
			var stdout, stderr bytes.Buffer
			command := newRootCommand()
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			args := []string{"-l", t.TempDir(), "tools"}
			if action != "inspect" {
				args = append(args, "dependencies", action, "--dependencies", "shared", "--dependencies", "shared")
			} else {
				args = append(args, action)
			}
			command.SetArgs(append(args, "--json"))
			err := command.Execute()
			if action == "inspect" {
				var report acquire.ToolsInspection
				if err != nil || json.Unmarshal(stdout.Bytes(), &report) != nil || !report.Complete || len(report.Dependencies) != 5 || calls != 0 {
					t.Fatalf("inspect %s err=%v calls=%d", &stdout, err, calls)
				}
			} else {
				var report acquire.DependencyChanges
				if err == nil || json.Unmarshal(stdout.Bytes(), &report) != nil || report.Status != "partial" || len(report.Receipts) != 3 || calls != 3 {
					t.Fatalf("mutation %s err=%v calls=%d", &stdout, err, calls)
				}
			}
		})
	}
}
