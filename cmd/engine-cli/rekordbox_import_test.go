package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/rekordbox"
)

func TestRekordboxImportCommandReviewsAppliesAndRejectsMissingSource(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			library := createTestLibrary(t)
			path := filepath.Join(t.TempDir(), "source.xml")
			media := (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(filepath.Join(t.TempDir(), "missing.wav"))}).String()
			if err := os.WriteFile(path, []byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="`+media+`"/></COLLECTION></DJ_PLAYLISTS>`), 0600); err != nil {
				t.Fatal(err)
			}
			run := func(flags ...string) (string, error) {
				t.Helper()
				command := newRootCommand()
				var out, diagnostics bytes.Buffer
				command.SetOut(&out)
				command.SetErr(&diagnostics)
				args := []string{"-l", library, "--no-backup", "migrate", "import", path, "--name", "Rekordbox set", "--missing-media", "skip"}
				if asJSON {
					args = append(args, "--json")
				}
				command.SetArgs(append(args, flags...))
				err := command.Execute()
				return out.String(), err
			}
			preview, err := run("--dry-run")
			if err != nil {
				t.Fatal(err)
			}
			var token string
			if asJSON {
				var review rekordbox.Review
				if err := json.Unmarshal([]byte(preview), &review); err != nil || !review.CanApply {
					t.Fatalf("preview=%s error=%v", preview, err)
				}
				token = review.PlanToken
			} else {
				_, rest, ok := strings.Cut(preview, "Plan token: ")
				if !ok {
					t.Fatalf("human preview omitted consent: %s", preview)
				}
				token, _, _ = strings.Cut(rest, "\n")
			}
			output, err := run("--plan-token", token)
			if err != nil || output == "" {
				t.Fatalf("apply output=%q error=%v", output, err)
			}
			if asJSON {
				var result rekordbox.Result
				if err := json.Unmarshal([]byte(output), &result); err != nil || result.RootID <= 0 || result.SkippedCount != 1 {
					t.Fatalf("apply receipt=%s error=%v", output, err)
				}
			} else if !strings.Contains(output, "1 unavailable sources skipped") {
				t.Fatalf("human receipt omitted actual outcome: %s", output)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			output, err = run("--dry-run")
			if err == nil || output != "" {
				t.Fatalf("failed native preview produced stdout=%q error=%v", output, err)
			}
		})
	}
}
