package sync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/migrate/sync"
)

func TestSyncState_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "sync_state.json")

	state := sync.NewState(stateFile)
	state.SetTrackStatus("/path/to/song1.mp3", sync.StatusSynced, "/target/song1.mp3", 1024)
	state.SetTrackStatus("/path/to/song2.mp3", sync.StatusPending, "", 2048)

	if err := state.Save(); err != nil {
		t.Fatalf("failed to save sync state: %v", err)
	}

	// Ensure file exists
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file should exist: %v", err)
	}

	// Load into a new state instance
	loadedState, err := sync.LoadState(stateFile)
	if err != nil {
		t.Fatalf("failed to load sync state: %v", err)
	}

	track1 := loadedState.GetTrackStatus("/path/to/song1.mp3")
	if track1.Status != sync.StatusSynced || track1.TargetLocation != "/target/song1.mp3" || track1.Size != 1024 {
		t.Fatalf("unexpected status for track1: %+v", track1)
	}

	track2 := loadedState.GetTrackStatus("/path/to/song2.mp3")
	if track2.Status != sync.StatusPending {
		t.Fatalf("unexpected status for track2: %+v", track2)
	}
}

func TestSyncState_ResetsCopyingStatusToPendingOnLoad(t *testing.T) {
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "sync_state.json")

	state := sync.NewState(stateFile)
	// Simulate an interrupted copy saved mid-flight
	state.SetTrackStatus("/path/to/interrupted.mp3", sync.StatusCopying, "/target/interrupted.mp3", 5000)

	if err := state.Save(); err != nil {
		t.Fatalf("failed to save sync state: %v", err)
	}

	loadedState, err := sync.LoadState(stateFile)
	if err != nil {
		t.Fatalf("failed to load sync state: %v", err)
	}

	track := loadedState.GetTrackStatus("/path/to/interrupted.mp3")
	if track.Status != sync.StatusPending {
		t.Fatalf("expected StatusCopying to be reset to StatusPending on load, got: %v", track.Status)
	}
}
