package suggest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSuggestionPendingRepairDoesNotInvokeAgent(t *testing.T) {
	_, lib := suggestionLibrary(t)
	fixture := askFixture(t, envelope([]modelItem{{ID: 1, Title: "Song"}}))
	dir := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte("interrupted publication intent"), 0600); err != nil {
		t.Fatal(err)
	}
	batches := 0
	result, err := Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, acquire.AskOptions{Harness: "codex"}, acquire.Client{}, func(Report) error {
		batches++
		return nil
	})
	if result != nil || progress.ErrorCode(err) != "smartlist_repair_pending" || batches != 0 {
		t.Fatalf("pending selection escaped: result=%+v batches=%d error=%v", result, batches, err)
	}
	if _, err := os.Stat(filepath.Join(fixture, "count")); !os.IsNotExist(err) {
		t.Fatalf("agent ran before coherent native selection: %v", err)
	}
}
