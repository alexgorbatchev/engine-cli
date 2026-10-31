package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func allowSmartlistReportCommands(t *testing.T) {
	t.Helper()
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
	}
}

func TestAutomaticSmartlistCLIReportsWithoutReplacingOutput(t *testing.T) {
	allowSmartlistReportCommands(t)
	for _, mode := range []string{"human", "agent", "progress JSON", "result JSON", "backup failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			}
			lib, database := reorderCommandLibrary(t)
			if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('orphan','Named saved list','Missing;','','','opaque')`); err != nil {
				t.Fatal(err)
			}
			args := []string{"-l", lib, "--no-backup", "playlist", "create", "Requested"}
			if mode == "backup failure" {
				if err := os.MkdirAll(filepath.Join(lib, ".engine-cli"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(lib, ".engine-cli", "backups"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"-l", lib, "playlist", "create", "Requested"}
			}
			if mode == "progress JSON" {
				args = append([]string{"--progress-json"}, args...)
			}
			if mode == "result JSON" {
				plan, err := playlist.PreviewReorder(t.Context(), database, lib, playlist.ReorderOptions{Playlist: "1", Rows: []int{0}, Drop: 3})
				if err != nil {
					t.Fatal(err)
				}
				args = []string{"-l", lib, "--no-backup", "playlist", "reorder", "1", "--rows", "0", "--drop", "3", "--plan-token", plan.PlanToken, "--json"}
			}
			var output, diagnostics bytes.Buffer
			command := newRootCommand()
			command.SetOut(&output)
			command.SetErr(&diagnostics)
			command.SetArgs(args)
			err := command.Execute()
			if mode == "backup failure" {
				if err == nil {
					t.Fatal("backup refusal lost")
				}
				var remaining, requested int
				if err := database.QueryRow(`SELECT count(*) FROM Smartlist WHERE listUuid='orphan'`).Scan(&remaining); err != nil || remaining != 0 {
					t.Fatalf("repair lost after backup refusal: %d %v", remaining, err)
				}
				if err := database.QueryRow(`SELECT count(*) FROM Playlist WHERE title='Requested'`).Scan(&requested); err != nil || requested != 0 {
					t.Fatalf("requested mutation ran after backup refusal: %d %v", requested, err)
				}
			} else if err != nil {
				t.Fatalf("CLI: %v stdout=%s stderr=%s", err, &output, &diagnostics)
			}
			if strings.Contains(output.String(), "Smartlist repair") || strings.Contains(output.String(), "Named saved list") {
				t.Fatalf("repair polluted requested stdout: %s", &output)
			}
			if mode == "progress JSON" {
				found := false
				for _, line := range strings.Split(strings.TrimSpace(diagnostics.String()), "\n") {
					var event progress.Event
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatalf("non-JSON progress: %s %v", line, err)
					}
					if event.Type == "smartlist_repair" && strings.Contains(string(event.Result), `"name":"Named saved list"`) {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing structured repair: %s", &diagnostics)
				}
			} else if !strings.Contains(diagnostics.String(), `remove: "Named saved list" ("orphan")`) || !strings.Contains(diagnostics.String(), "removed 1") {
				t.Fatalf("missing named repair: %s", &diagnostics)
			}
			if mode == "result JSON" {
				var result playlist.ReorderResult
				if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.PlaylistID != 1 || len(result.TrackIDs) != 3 || result.TrackIDs[2] != 1 {
					t.Fatalf("requested JSON altered: %s %v", &output, err)
				}
			} else if mode != "backup failure" && !strings.Contains(output.String(), "Requested") {
				t.Fatalf("requested output lost: %s", &output)
			}
		})
	}
}

func TestSmartlistCLIQuotesNativeIdentity(t *testing.T) {
	allowSmartlistReportCommands(t)
	for _, shape := range []struct{ operation, agent string }{{"automatic", "0"}, {"automatic", "1"}, {"preview", "0"}, {"preview", "1"}} {
		t.Run(shape.operation+"/agent="+shape.agent, func(t *testing.T) {
			t.Setenv("AGENT", shape.agent)
			lib, database := reorderCommandLibrary(t)
			identity := "damaged\n\x1b[31midentity"
			if _, err := database.Exec(`INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES(?,?,'Missing;','','','opaque')`, identity, "Named saved list"); err != nil {
				t.Fatal(err)
			}
			args := []string{"-l", lib, "--no-backup", "playlist", "create", "Requested"}
			if shape.operation == "preview" {
				args = []string{"-l", lib, "library", "smartlist-repair", "preview"}
			}
			var output, diagnostics bytes.Buffer
			command := newRootCommand()
			command.SetOut(&output)
			command.SetErr(&diagnostics)
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatalf("CLI: %v", err)
			}
			text := output.String() + diagnostics.String()
			if strings.Contains(text, identity) || strings.ContainsRune(text, '\x1b') || !strings.Contains(text, fmt.Sprintf("%q", identity)) {
				t.Fatalf("native identity was not safely quoted: %q", text)
			}
			var remaining int
			if err := database.QueryRow(`SELECT count(*) FROM Smartlist WHERE listUuid=?`, identity).Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if (shape.operation == "preview" && remaining != 1) || (shape.operation == "automatic" && remaining != 0) {
				t.Fatalf("output escaping changed native identity semantics: %d", remaining)
			}
		})
	}
}
