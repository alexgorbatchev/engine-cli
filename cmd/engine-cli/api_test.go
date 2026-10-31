package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

func TestCLIProgressJSON(t *testing.T) {
	lib := createTestLibrary(t)
	cmd := newRootCommand()
	var out, events bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&events)
	cmd.SetArgs([]string{"-l", lib, "--progress-json", "playlist", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(events.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("events=%s", events.String())
	}
	previous := 0.0
	for _, line := range lines {
		var event progress.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Percent < previous || event.Percent > 100 {
			t.Fatalf("bad percentage: %+v", event)
		}
		previous = event.Percent
	}
	if previous != 100 {
		t.Fatalf("final percent=%v", previous)
	}
}

func TestAPICatalogCoversCLI(t *testing.T) {
	s, err := api.New(api.Config{LibraryDir: createTestLibrary(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest(http.MethodGet, "http://localhost/v1/operations", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var operations []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &operations); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, op := range operations {
		names[op.Name] = true
	}
	root := newRootCommand()
	var check func(*cobra.Command)
	check = func(cmd *cobra.Command) {
		if cmd.HasSubCommands() {
			for _, child := range cmd.Commands() {
				check(child)
			}
			return
		}
		name := strings.ReplaceAll(strings.TrimPrefix(cmd.CommandPath(), root.Name()+" "), " ", ".")
		if !names[name] {
			t.Errorf("CLI operation %s missing from HTTP catalog", name)
		}
	}
	for _, group := range root.Commands() {
		switch group.Name() {
		case "api", "skill":
			// Server startup and skill installation instructions are local CLI commands.
		default:
			check(group)
		}
	}
}

func TestAPICommandHelp(t *testing.T) {
	out, err := executeCommand("api", "serve", "--help")
	if err != nil || !strings.Contains(out, "--port") || !strings.Contains(out, "--allow-origin") || !strings.Contains(out, "undo history") {
		t.Fatalf("help=%s err=%v", out, err)
	}
}
