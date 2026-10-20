package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/migrate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
)

func TestMigrateHelp(t *testing.T) {
	for _, args := range [][]string{{"migrate", "--help"}, {"migrate", "export", "--help"}} {
		c := newRootCommand()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetArgs(args)
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "export") {
			t.Fatalf("missing export: %s", out.String())
		}
		if len(args) == 3 && (!strings.Contains(out.String(), "--source") || !strings.Contains(out.String(), "--target")) {
			t.Fatalf("missing export flags: %s", out.String())
		}
	}
}

func migrationLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "Engine Library")
	if err := engine.Export(lib.Library{DatabaseUUID: "cli", Songs: []lib.Song{{SongID: 1, Path: "../song.mp3", Title: "Test", Filetype: "mp3"}}}, source, engine.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestMigrateExportResultsAndProgress(t *testing.T) {
	source := migrationLibrary(t)
	t.Chdir(t.TempDir())
	for _, mode := range []string{"human", "agent", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			}
			c := newRootCommand()
			var out, events bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&events)
			args := []string{"-l", source, "migrate", "export", "--progress-json"}
			if mode == "json" {
				args = append(args, "--json", "--source", source, "--target", "custom")
			}
			c.SetArgs(args)
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			if mode == "json" {
				var r migrate.Report
				if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Copied != 1 || !filepath.IsAbs(r.XML) {
					t.Fatalf("result=%q err=%v", out.String(), err)
				}
			} else if !strings.Contains(out.String(), "failed: 0") {
				t.Fatalf("result=%q", out.String())
			}
			if !strings.Contains(events.String(), `"type":"complete"`) || !strings.Contains(events.String(), `"percent":100`) {
				t.Fatalf("incomplete progress: %s", events.String())
			}
		})
	}
}

func TestMigrateRejectsArgumentsAndOutputFailures(t *testing.T) {
	for _, args := range [][]string{{"migrate", "extra"}, {"migrate", "export", "extra"}, {"migrate", "export", "--source", filepath.Join(t.TempDir(), "missing")}} {
		c := newRootCommand()
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	source := migrationLibrary(t)
	closed, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, jsonOutput := range []bool{false, true} {
		c := newRootCommand()
		c.SetOut(closed)
		args := []string{"migrate", "export", "-s", source, "-t", t.TempDir()}
		if jsonOutput {
			args = append(args, "--json")
		}
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("output failure lost")
		}
	}
}

func TestMigrateErrorsFoldHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := newRootCommand()
	c.SetArgs([]string{"migrate", "export", "--source", filepath.Join(home, "missing")})
	err := c.Execute()
	if err == nil || strings.Contains(err.Error(), home) || !strings.Contains(err.Error(), "~/missing") {
		t.Fatalf("unfolded error=%v", err)
	}
}

func TestMigrateCLIStreamsIntermediateProgress(t *testing.T) {
	c := newRootCommand()
	var out, events bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&events)
	c.SetArgs([]string{"migrate", "export", "--source", migrationLibrary(t), "--target", t.TempDir(), "--json", "--progress-json"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&events)
	var previous float64
	var sequence uint64
	var jobID string
	phases := make(map[string]bool)
	var sawBytes, completed bool
	for {
		var event progress.Event
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if jobID == "" {
			jobID = event.JobID
		}
		if completed || event.JobID != jobID || event.JobID == "" || event.Sequence <= sequence || event.Percent < previous || (event.Percent == 100 && event.Type != "complete") {
			t.Fatalf("invalid progress=%+v", event)
		}
		previous, sequence = event.Percent, event.Sequence
		phases[event.Phase] = true
		if len(event.Metadata) > 0 {
			var data struct {
				Copied int64 `json:"copied_bytes"`
				Total  int64 `json:"total_bytes"`
			}
			if err := json.Unmarshal(event.Metadata, &data); err != nil {
				t.Fatal(err)
			}
			sawBytes = data.Copied == 5 && data.Total == 5
		}
		completed = event.Type == "complete"
	}
	if !completed || previous != 100 || !sawBytes || !phases["migrate.read"] || !phases["migrate.copy"] || !phases["migrate.xml"] {
		t.Fatalf("incomplete progress: phases=%v bytes=%v percent=%v", phases, sawBytes, previous)
	}
}
