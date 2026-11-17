package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/testdesktop"
)

// Handoff unit tests use a deterministic Desktop observation, independent of the
// machine. CLI binaries they run are built with testdesktopBuildTags.
func TestMain(m *testing.M) {
	querySystem = testdesktop.Closed
	os.Exit(m.Run())
}

// testdesktopBuildTags builds a CLI whose mutation guard observes a closed
// Engine DJ Desktop, as the in-process stub does.
const testdesktopBuildTags = "-tags=testdesktop"

func TestHandoffRefusesWhileDesktopRuns(t *testing.T) {
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	calls := 0
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		calls++
		return testdesktop.Report(true), nil
	}
	root := filepath.Join(t.TempDir(), "fixtures")
	var refusal *systemcheck.Error
	if err := seed(t.Context(), root, inputs{}, golden{}); !errors.As(err, &refusal) || refusal.Code != "engine_desktop_running" {
		t.Fatalf("seed while Desktop runs: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("refused seed created its root: %v", err)
	}
	if err := verify(t.Context(), root, inputs{}, golden{}, "", "seed"); !errors.As(err, &refusal) || refusal.Code != "engine_desktop_running" {
		t.Fatalf("verify while Desktop runs: %v", err)
	}
	if calls != 2 {
		t.Fatalf("Desktop observed %d times, want 2", calls)
	}
}
