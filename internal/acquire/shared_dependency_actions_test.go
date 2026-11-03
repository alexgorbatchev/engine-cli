package acquire

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
)

func TestSharedMutationRejectsInvalidSelectionBeforeChanges(t *testing.T) {
	for _, tt := range []struct {
		action string
		names  []string
	}{
		{"invalid", []string{"shared"}}, {"install", nil},
		{"update", []string{"shared", "unknown"}}, {"install", []string{" shared"}},
	} {
		client := sharedRunner(t, func(string, []string) ([]byte, error) {
			t.Fatal("invalid selection started mutation")
			return nil, nil
		})
		if result, err := client.ChangeDependencies(t.Context(), tt.action, tt.names); err == nil || result != nil {
			t.Fatalf("invalid request result=%+v err=%v", result, err)
		}
	}
}

func TestSharedMutationRequiresExplicitChangeReceipt(t *testing.T) {
	client := sharedRunner(t, func(string, []string) ([]byte, error) {
		return []byte(`{"status":"ok","changed":[],"dependencies":[]}`), nil
	})
	result, err := client.ChangeDependencies(t.Context(), "install", []string{"shared"})
	if err == nil || result.Status != "error" || len(result.Receipts) != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, receipt := range result.Receipts {
		if receipt.Status != "error" || receipt.ErrorCode != "tool_failed" {
			t.Fatalf("unconfirmed change %+v", receipt)
		}
	}
}

func TestSharedMutationIncompleteInspectionStillRepairsManagedTool(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installs := 0
	client := sharedRunner(t, func(string, []string) ([]byte, error) { return nil, errors.New("unexpected mutation") })
	base := client.Runner
	client.Runner = func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "fetch-mix" && slices.Equal(args, []string{"--version"}) && installs == 0 {
			return []byte("0.1"), nil
		}
		return base(ctx, tool, args...)
	}
	client.Installer = godeps.InstallerFunc(func(context.Context, string) error { installs++; return nil })
	result, err := client.ChangeDependencies(t.Context(), "update", []string{"fetch-mix"})
	if progress.ErrorCode(err) != dependencyCheckIncomplete || result.ErrorCode != dependencyCheckIncomplete || result.Status != "partial" || installs != 1 || len(result.Receipts) != 1 || result.Receipts[0].Status != "success" {
		t.Fatalf("repair lost or coverage overstated: result=%+v installs=%d err=%v", result, installs, err)
	}
}

func TestSharedMutationIncludesManagedAndPrivateCopies(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installs, private := 0, 0
	client := sharedRunner(t, func(string, []string) ([]byte, error) { return nil, errors.New("unexpected mutation") })
	base := client.Runner
	client.Runner = func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "fetch-mix" && slices.Equal(args, []string{"--json", "dependencies"}) {
			return []byte(`{"status":"ok","changed":[],"dependencies":[{"name":"fetch-track","installed":true,"detectedVersion":"9.0","minVersion":"1.0","satisfied":true}]}`), nil
		}
		if tool == "fetch-mix" && slices.Equal(args, []string{"--json", "dependencies", "update", "fetch-track"}) {
			private++
			return []byte(`{"status":"ok","changed":["fetch-track"],"dependencies":[]}`), nil
		}
		return base(ctx, tool, args...)
	}
	client.Installer = godeps.InstallerFunc(func(context.Context, string) error { installs++; return nil })
	result, err := client.ChangeDependencies(t.Context(), "update", []string{"fetch-track"})
	if err != nil || result.Status != "success" || installs != 1 || private != 1 || len(result.Receipts) != 2 {
		t.Fatalf("result=%+v installs=%d private=%d err=%v", result, installs, private, err)
	}
	if result.Receipts[0].Status != "success" || result.Receipts[1].Status != "current" {
		t.Fatalf("actual receipts=%+v", result.Receipts)
	}
}

func TestSharedInspectionAndMutationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := Client{Runner: func(context.Context, string, ...string) ([]byte, error) { cancel(); return nil, ctx.Err() }}
	report, err := client.InspectTools(ctx)
	if !errors.Is(err, context.Canceled) || report.Complete || len(report.Tools) != 0 {
		t.Fatalf("partial inspection=%+v err=%v", report, err)
	}
	result, err := client.ChangeDependencies(ctx, "update", []string{"shared"})
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("cancelled preflight=%+v err=%v", result, err)
	}
}
