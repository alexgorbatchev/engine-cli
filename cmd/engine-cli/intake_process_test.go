package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/api"
)

func TestIntakeCLIProcessPreparesDurablyWithoutImport(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			lib := intakeCLILibrary(t)
			t.Setenv("AGENT", mode)
			source := filepath.Join(t.TempDir(), "incoming.wav")
			if err := os.WriteFile(source, []byte("original source"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"pause"}, {"enqueue", source}, {"process", "--json"}} {
				out, err := executeIntakeCLI(append([]string{"-l", lib, "intake"}, args...)...)
				if err != nil {
					t.Fatalf("%v: %s %v", args, out, err)
				}
				if args[0] == "process" {
					var view api.IntakeView
					if err := json.Unmarshal([]byte(out), &view); err != nil || !view.Control.Paused || len(view.Items) != 1 || view.Items[0].Status != "queued" {
						t.Fatalf("paused processing changed queue: %s %v", out, err)
					}
				}
			}
			if _, err := executeIntakeCLI("-l", lib, "intake", "resume"); err != nil {
				t.Fatal(err)
			}
			out, err := executeIntakeCLI("-l", lib, "intake", "process")
			want := "Item 1: staged"
			if mode == "1" {
				want = "id:1 status:staged"
			}
			if err != nil || !strings.Contains(out, want) {
				t.Fatalf("foreground processing returned before staging: %s %v", out, err)
			}
			out, err = executeIntakeCLI("-l", lib, "intake", "list", "--json")
			var view api.IntakeView
			if err != nil || json.Unmarshal([]byte(out), &view) != nil || len(view.Items) != 1 || view.Items[0].Status != "staged" || view.Items[0].ContentHash == "" || view.Items[0].HistoryID != "" {
				t.Fatalf("staging did not survive CLI shutdown: %s %v", out, err)
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != "original source" {
				t.Fatalf("processing changed original: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "history")); !os.IsNotExist(err) {
				t.Fatalf("preparation created collection History: %v", err)
			}
		})
	}
}
