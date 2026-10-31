package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/performance"
	"github.com/nateranda/djtools/engine"
)

func cueCommandLibrary(t *testing.T) (string, *sql.DB, *performance.Saved) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	library := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(1,'offline.wav','Cue fixture')"); err != nil {
		t.Fatal(err)
	}
	data := binary.BigEndian.AppendUint64(nil, math.Float64bits(48000))
	data = binary.BigEndian.AppendUint64(data, 480000)
	blob, err := engine.QCompress(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", blob); err != nil {
		t.Fatal(err)
	}
	return library, d, readCommandCues(t, d, library)
}

func readCommandCues(t *testing.T, d *sql.DB, library string) *performance.Saved {
	t.Helper()
	saved, err := performance.Inspect(t.Context(), d, library, "1")
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestCueCommandsWriteInspectClearAndUndo(t *testing.T) {
	library, d, before := cueCommandLibrary(t)
	output, err := executeCommand("--lib-dir", library, "track", "cue", "inspect", "1")
	if err != nil || !strings.Contains(output, "Revision: "+before.Revision) || !strings.Contains(output, "Hot cues: 0") {
		t.Fatalf("inspect=%s error=%v", output, err)
	}
	args := []string{"--lib-dir", library, "--no-backup", "track", "cue", "set", "1", "--slot", "h", "--revision", before.Revision, "--position", "1.23456789", "--name", "Outro", "--color", "#12ABEF"}
	output, err = executeCommand(args...)
	if err != nil || !strings.Contains(output, "Hot cue: H") || !strings.Contains(output, "Changed: true") {
		t.Fatalf("set=%s error=%v", output, err)
	}
	saved := readCommandCues(t, d, library)
	if len(saved.Cues) != 1 || saved.Cues[0].Slot != 8 || saved.Cues[0].Position != 1.23456789 || saved.Cues[0].Name != "Outro" || saved.Cues[0].Color != "#12ABEF" {
		t.Fatalf("saved=%+v", saved)
	}
	command := newRootCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"--lib-dir", library, "track", "cue", "inspect", "1", "--json"})
	var inspected performance.Saved
	if err := command.Execute(); err != nil {
		t.Fatalf("inspect JSON=%s diagnostics=%s error=%v", stdout.String(), stderr.String(), err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Revision != saved.Revision || len(inspected.Cues) != 1 || inspected.Cues[0] != saved.Cues[0] {
		t.Fatalf("inspect=%+v saved=%+v", inspected, saved)
	}
	output, err = executeCommand("--lib-dir", library, "--no-backup", "track", "cue", "clear", "1", "--slot", "H", "--revision", saved.Revision)
	if err != nil || !strings.Contains(output, "Changed: true") {
		t.Fatalf("clear=%s error=%v", output, err)
	}
	if current := readCommandCues(t, d, library); len(current.Cues) != 0 {
		t.Fatalf("clear left cues: %+v", current)
	}
	if _, err := history.Undo(t.Context(), library, "", history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if current := readCommandCues(t, d, library); len(current.Cues) != 1 || current.Cues[0] != saved.Cues[0] {
		t.Fatalf("undo did not restore cue: %+v", current)
	}
}

func TestCueCommandsRejectMissingReviewAndInvalidSlotsWithoutWriting(t *testing.T) {
	for _, arguments := range [][]string{
		{"set", "1", "--slot", "A", "--position", "0", "--color", "#FFFFFF"},
		{"set", "1", "--slot", "I", "--revision", "reviewed", "--position", "0", "--color", "#FFFFFF"},
		{"set", "1", "--slot", "AA", "--revision", "reviewed", "--position", "0", "--color", "#FFFFFF"},
		{"set", "1", "--slot", "A", "--revision", "reviewed", "--color", "#FFFFFF"},
		{"clear", "1", "--slot", "A"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			library, d, before := cueCommandLibrary(t)
			output, err := executeCommand(append([]string{"--lib-dir", library, "track", "cue"}, arguments...)...)
			if err == nil {
				t.Fatalf("accepted incomplete cue command: %s", output)
			}
			if current := readCommandCues(t, d, library); current.Revision != before.Revision {
				t.Fatal("rejected command changed saved performance")
			}
			for _, folder := range []string{"backups", "history"} {
				if _, err := os.Stat(filepath.Join(library, ".engine-cli", folder)); !os.IsNotExist(err) {
					t.Fatalf("rejected command created %s: %v", folder, err)
				}
			}
		})
	}
}

func TestCueInspectReportsNativeOutputFailure(t *testing.T) {
	library, _, _ := cueCommandLibrary(t)
	output, err := os.CreateTemp(t.TempDir(), "closed-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	var diagnostics bytes.Buffer
	command.SetOut(output)
	command.SetErr(&diagnostics)
	command.SetArgs([]string{"--lib-dir", library, "track", "cue", "inspect", "1"})
	if err := command.Execute(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed output error=%v diagnostics=%s", err, diagnostics.String())
	}
}
