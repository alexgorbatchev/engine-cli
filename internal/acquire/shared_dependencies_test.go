package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func sharedToolReports() []*Tool {
	var tools []*Tool
	for _, name := range []string{"agent-ask-cli", "fetch-mix", "fetch-track", "tag-track"} {
		tools = append(tools, &Tool{Name: name, Version: "9.0.0", MinimumVersion: requirements[name].minVersion,
			Available: true, Satisfied: true, NativeJSON: true,
			Dependencies: &Dependencies{Status: "ok", Dependencies: []Dependency{}, Changed: []string{}}})
	}
	return tools
}

func sharedByName(t *testing.T, report *ToolsInspection, name string) *SharedDependency {
	t.Helper()
	for _, dep := range report.Dependencies {
		if dep.Name == name {
			return dep
		}
	}
	t.Fatalf("dependency %q absent from %+v", name, report)
	return nil
}

func TestAggregateSharedRequirements(t *testing.T) {
	for _, tt := range []struct{ name, first, second, want string }{
		{"minor", "1.1", "1.2", "1.2"},
		{"numeric segments", "1.9", "1.10", "1.10"},
		{"date", "2024.08.01", "2024.10.01", "2024.10.01"},
		{"equal zero padding", "2024.08.01", "2024.8.1", "2024.08.01"},
		{"equal short", "1.2", "1.2.0", "1.2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tools := sharedToolReports()
			for i, minimum := range []string{tt.first, tt.second, tt.first} {
				tools[i+1].Dependencies.Dependencies = []Dependency{{Name: "shared", Installed: true, DetectedVersion: "9999.0", MinVersion: minimum, Satisfied: true}}
			}
			report := aggregateTools(tools)
			dep := sharedByName(t, report, "shared")
			if dep.MinimumVersion != tt.want || !dep.Satisfied || !dep.Verified || len(dep.Contexts) != 3 || len(dep.RequiredBy) != 3 || len(dep.InstalledVersions) != 1 {
				t.Fatalf("aggregate=%+v", dep)
			}
		})
	}
}

func TestAggregateRequiresHighestMinimumInEveryContext(t *testing.T) {
	tools := sharedToolReports()
	tools[1].Dependencies.Dependencies = []Dependency{{Name: "shared", Installed: true, DetectedVersion: "1.5", MinVersion: "1.1", Satisfied: true}}
	tools[2].Dependencies.Dependencies = []Dependency{{Name: "shared", Installed: true, DetectedVersion: "2.0", MinVersion: "2.0", Satisfied: true}}
	dep := sharedByName(t, aggregateTools(tools), "shared")
	if dep.Satisfied || !dep.Verified || !dep.Installed || dep.MinimumVersion != "2.0" || len(dep.InstalledVersions) != 2 {
		t.Fatalf("lower-requirement context falsely ready: %+v", dep)
	}
}

func TestAggregateUnknownAndFailedEvidence(t *testing.T) {
	for _, name := range []string{"empty minimum", "unreadable minimum", "missing version", "unreadable version", "item error", "parent error", "missing report"} {
		t.Run(name, func(t *testing.T) {
			tools := sharedToolReports()
			tools[1].Dependencies.Dependencies = []Dependency{{Name: "shared", Installed: true, DetectedVersion: "2.0", MinVersion: "1.0", Satisfied: true}}
			dep := &tools[1].Dependencies.Dependencies[0]
			switch name {
			case "empty minimum":
				dep.MinVersion = ""
			case "unreadable minimum":
				dep.MinVersion = "unknown"
			case "missing version":
				dep.DetectedVersion = ""
			case "unreadable version":
				dep.DetectedVersion = "unknown"
			case "item error":
				dep.ErrorCode = "tool_failed"
			case "parent error":
				tools[1].Dependencies.ErrorCode = "tool_failed"
			case "missing report":
				tools[2].Dependencies = nil
			}
			got := sharedByName(t, aggregateTools(tools), "shared")
			if got.Satisfied || got.Verified || got.ErrorCode == "" {
				t.Fatalf("unverified evidence became ready: %+v", got)
			}
		})
	}
}

func TestManagedToolMergesItsRootRequirement(t *testing.T) {
	tools := sharedToolReports()
	tools[1].Dependencies.Dependencies = []Dependency{{Name: "fetch-track", Installed: true, DetectedVersion: "1.5.0", MinVersion: "1.4.0", Satisfied: true}}
	dep := sharedByName(t, aggregateTools(tools), "fetch-track")
	if dep.MinimumVersion != requirements["fetch-track"].minVersion || dep.Satisfied || len(dep.Contexts) != 2 || !slices.Contains(dep.RequiredBy, managedApp) {
		t.Fatalf("managed requirement not merged: %+v", dep)
	}
}

func TestAggregateNativeOutdatedDiagnosticsRetainVerifiedEvidence(t *testing.T) {
	tools := sharedToolReports()
	tools[1].Dependencies = &Dependencies{Status: "error", ErrorCode: "tool_failed", Dependencies: []Dependency{
		{Name: "shared", Installed: true, DetectedVersion: "1.9", MinVersion: "1.10", ErrorCode: "tool_failed"},
	}, Changed: []string{}}
	report := aggregateTools(tools)
	dep := sharedByName(t, report, "shared")
	if !report.Complete || !dep.Verified || dep.Satisfied || dep.ErrorCode != "" {
		t.Fatalf("known outdated version became a failed probe: report=%+v dep=%+v", report, dep)
	}
	tools[1].Dependencies.Dependencies[0].DetectedVersion = ""
	report = aggregateTools(tools)
	dep = sharedByName(t, report, "shared")
	if report.Complete || dep.Verified || dep.Satisfied {
		t.Fatalf("failed version probe became verified: report=%+v dep=%+v", report, dep)
	}
}

func TestAggregateDistinguishesParentFailureFromKnownOutdated(t *testing.T) {
	for _, tt := range []struct {
		name         string
		dependencies []Dependency
		complete     bool
	}{
		{"unexplained failure", []Dependency{{Name: "shared", Installed: true, DetectedVersion: "2.0", MinVersion: "1.0", Satisfied: true}}, false},
		{"empty failure", []Dependency{}, false},
		{"known outdated", []Dependency{{Name: "shared", Installed: true, DetectedVersion: "1.0", MinVersion: "2.0"}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tools := sharedToolReports()
			tools[1].Dependencies = &Dependencies{Status: "error", Dependencies: tt.dependencies, Changed: []string{}}
			report := aggregateTools(tools)
			if report.Complete != tt.complete {
				t.Fatalf("complete=%v want %v", report.Complete, tt.complete)
			}
			if len(tt.dependencies) > 0 {
				dep := sharedByName(t, report, "shared")
				if dep.Verified != tt.complete || dep.Satisfied || (!tt.complete && dep.ErrorCode != dependencyCheckIncomplete) {
					t.Fatalf("parent error evidence %+v", dep)
				}
			}
		})
	}
}

func sharedRunner(t *testing.T, action func(string, []string) ([]byte, error)) Client {
	t.Helper()
	return Client{Runner: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"--version"}) {
			return []byte(tool + " version 9.0.0"), nil
		}
		if slices.Equal(args, []string{"--json", "dependencies"}) {
			deps := []Dependency{}
			if tool != "agent-ask-cli" {
				deps = append(deps, Dependency{Name: "shared", Installed: tool != "fetch-mix", DetectedVersion: "1.0", MinVersion: "2.0"})
			}
			return json.Marshal(Dependencies{Status: "ok", Dependencies: deps, Changed: []string{}})
		}
		return action(tool, args)
	}}
}

func TestSharedMutationCoordinatesCopiesAndPreservesReceipts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial"}[partial], func(t *testing.T) {
			var calls []string
			client := sharedRunner(t, func(tool string, args []string) ([]byte, error) {
				calls = append(calls, tool)
				wantAction := "update"
				if tool == "fetch-mix" {
					wantAction = "install"
				}
				if !slices.Equal(args, []string{"--json", "dependencies", wantAction, "shared"}) {
					t.Fatalf("args=%v", args)
				}
				if partial && tool == "fetch-track" {
					return []byte(`{"status":"error","changed":[],"dependencies":[],"error":"failed"}`), errors.New("failed")
				}
				// An unrelated dependency failed verification after this target changed.
				return []byte(`{"status":"error","changed":["shared"],"dependencies":[],"error":"unrelated"}`), errors.New("unrelated")
			})
			result, err := client.ChangeDependencies(context.Background(), "update", []string{"shared", "shared"})
			if (err != nil) != partial || len(result.Receipts) != 3 || !reflect.DeepEqual(calls, []string{"fetch-mix", "fetch-track", "tag-track"}) {
				t.Fatalf("result=%+v err=%v calls=%v", result, err, calls)
			}
			wantStatus := "success"
			if partial {
				wantStatus = "partial"
			}
			if result.Status != wantStatus {
				t.Fatalf("status=%s", result.Status)
			}
		})
	}
}

func TestSharedMutationCancellationRetainsCompletedContexts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := sharedRunner(t, func(tool string, args []string) ([]byte, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		return []byte(`{"status":"ok","changed":["shared"],"dependencies":[]}`), nil
	})
	result, err := client.ChangeDependencies(ctx, "update", []string{"shared"})
	if !errors.Is(err, context.Canceled) || calls != 2 || len(result.Receipts) != 2 || result.Receipts[0].Status != "success" || result.Status != "partial" {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestToolReadUsesItsManagedDependencyPathWithoutChangingEngine(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	managed := filepath.Join(root, "fetch-track", "bin")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "shared"), []byte("#!/bin/sh\nprintf 'private'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "fetch-track"), []byte("#!/bin/sh\nshared\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := runCommand(context.Background(), "fetch-track", "--json", "dependencies")
	if err != nil || string(output) != "private" || os.Getenv("PATH") != bin {
		t.Fatalf("output=%q err=%v PATH=%s", output, err, os.Getenv("PATH"))
	}
}
