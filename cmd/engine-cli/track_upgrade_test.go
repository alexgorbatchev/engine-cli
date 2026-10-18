package main

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/track"
	"github.com/spf13/cobra"
)

func TestTrackUpgradeHelp(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			out, err := executeCommand("track", "upgrade", "--help")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"upgrade <tr|id|path>", "--query", "--keep-timing", "--clear-timing", "--no-backup"} {
				if !strings.Contains(out, want) {
					t.Errorf("help missing %q: %s", want, out)
				}
			}
		})
	}
}

func TestPrintTrackUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	result := &track.UpgradeResult{
		Track:         &db.Track{ID: 42, Title: sql.NullString{String: "Curated title", Valid: true}, Path: sql.NullString{String: filepath.Join(home, "replacement.flac"), Valid: true}},
		OriginalPath:  filepath.Join(home, "original.mp3"),
		TimingCleared: true,
	}
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)
			if err := printTrackUpgrade(cmd, result); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"~/replacement.flac", "~/original.mp3"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output missing %q: %s", want, out.String())
				}
			}
			if mode == "1" && !strings.Contains(out.String(), "timing_cleared: true\nreanalysis_required: true") {
				t.Fatal(out.String())
			}
			cmd.SetOut(upgradeFailWriter{})
			if err := printTrackUpgrade(cmd, result); err == nil {
				t.Fatal("expected output error")
			}
		})
	}
}

type upgradeFailWriter struct{}

func (upgradeFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestDisplayPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "dj")
	t.Setenv("HOME", home)
	for _, tt := range []struct{ path, want string }{
		{home, "~"},
		{filepath.Join(home, "Music"), "~/Music"},
		{home + "-other/Music", home + "-other/Music"},
		{"relative.flac", "relative.flac"},
	} {
		if got := displayPath(tt.path); got != tt.want {
			t.Errorf("displayPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
	t.Setenv("HOME", "")
	if got := displayPath("relative.flac"); got != "relative.flac" {
		t.Fatal(got)
	}
}

func TestTrackUpgradeErrors(t *testing.T) {
	t.Setenv("AGENT", "0")
	t.Setenv("PATH", t.TempDir())
	lib := createTestLibrary(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"missing argument", []string{"track", "upgrade"}, "accepts 1 arg"},
		{"extra argument", []string{"track", "upgrade", "1", "2"}, "accepts 1 arg"},
		{"conflicting timing flags", []string{"track", "upgrade", "1", "--keep-timing", "--clear-timing"}, "none of the others can be"},
		{"missing track", []string{"--no-backup", "-l", lib, "track", "upgrade", "999"}, "track"},
		{"missing fetch executable", []string{"--no-backup", "-l", lib, "track", "upgrade", "1"}, "fetch-track"},
		{"backup failure", []string{"-l", filepath.Join(t.TempDir(), "missing"), "track", "upgrade", "1"}, "automatic backup before track upgrade"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeCommand(tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestTrackUpgradeCreatesBackupBeforeDownload(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			lib := createTestLibrary(t)
			data := t.TempDir()
			t.Setenv("XDG_DATA_HOME", data)
			out, err := executeCommand("-l", lib, "track", "upgrade", "1")
			if err == nil {
				t.Fatal("expected unavailable Fetch Track CLI error")
			}
			if !strings.Contains(out, "snapshot") {
				t.Fatalf("missing backup output: %s", out)
			}
			entries, err := os.ReadDir(filepath.Join(data, "engine-cli", "backups"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("backup entries = %v, error = %v", entries, err)
			}
		})
	}
}

func TestTrackUpgradeTimingChoice(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	lib := createTestLibrary(t)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE PerformanceData (trackId INTEGER, quickCues BLOB); INSERT INTO PerformanceData VALUES (1, x'0102')"); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"", "--keep-timing", "--clear-timing"} {
		t.Run(flag, func(t *testing.T) {
			args := []string{"--no-backup", "-l", lib, "track", "upgrade", "1"}
			if flag != "" {
				args = append(args, flag)
			}
			_, err := executeCommand(args...)
			want := "fetch-track"
			if flag == "" {
				want = "--keep-timing"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
			var count int
			if err := database.QueryRow("SELECT COUNT(*) FROM PerformanceData").Scan(&count); err != nil || count != 1 {
				t.Fatalf("failed acquisition changed timing: count=%d err=%v", count, err)
			}
		})
	}
}
