package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
)

type suggestionCapture struct {
	mu sync.Mutex
	bytes.Buffer
}

func (w *suggestionCapture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Buffer.Write(p)
}
func (w *suggestionCapture) snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Buffer.String()
}

func TestSuggestionCLIStreamsBatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	for _, asJSON := range []bool{false, true} {
		for _, outcome := range []string{"success", "failed", "cancelled"} {
			t.Run(fmt.Sprintf("json=%v %s", asJSON, outcome), func(t *testing.T) {
				lib := createTestLibrary(t)
				d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = d.Exec(`WITH RECURSIVE n(id) AS (SELECT 2 UNION ALL SELECT id+1 FROM n WHERE id<26) INSERT INTO Track(id,title,artist) SELECT id,'Artist - Song','Artist' FROM n`)
				if closeErr := d.Close(); err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
				dir := t.TempDir()
				var ids []string
				for id := 1; id <= 26; id++ {
					ids = append(ids, fmt.Sprint(id))
				}
				for start := 1; start <= 26; start += 25 {
					var items []map[string]any
					for id := start; id < start+25 && id <= 26; id++ {
						artist := "Artist"
						if id == 1 {
							artist = "Sample Artist"
						}
						items = append(items, map[string]any{"id": id, "title": "Song", "artist": artist, "reason": "Cleanup"})
					}
					data, err := json.Marshal(map[string]any{"harness": "codex", "output_kind": "json", "output": map[string]any{"items": items}})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(start)), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				script := `#!/bin/sh
if [ "$1" = --version ]; then echo 1.0.0; exit; fi
cat > /dev/null
cd "$ENGINE_CLI_SUGGEST_FIXTURE"
if [ ! -f first ]; then : > first; cat 1; exit; fi
trap 'exit 130' INT
: > waiting
while [ ! -f release ]; do sleep 0.01; done
if [ "$ENGINE_CLI_SUGGEST_OUTCOME" = failed ]; then
 echo '{"error":{"code":"harness_failed","message":"Second batch failed"}}' >&2
 exit 1
fi
cat 26
`
				if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AGENT", "0")
				t.Setenv("ENGINE_CLI_SUGGEST_FIXTURE", dir)
				t.Setenv("ENGINE_CLI_SUGGEST_OUTCOME", outcome)
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cmd := newRootCommand()
				cmd.SetContext(ctx)
				out := &suggestionCapture{}
				cmd.SetOut(out)
				cmd.SetErr(io.Discard)
				args := append([]string{"-l", lib, "track", "suggest", "--harness", "codex"}, ids...)
				if asJSON {
					args = append(args, "--json")
				}
				cmd.SetArgs(args)
				done := make(chan error, 1)
				go func() { done <- cmd.Execute(); close(done) }()
				release := func() {
					if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
						t.Error(err)
					}
				}
				defer func() { cancel(); release(); <-done }()
				deadline := time.NewTimer(10 * time.Second)
				defer deadline.Stop()
				for {
					if _, err := os.Stat(filepath.Join(dir, "waiting")); err == nil {
						break
					}
					select {
					case err := <-done:
						t.Fatal("command ended before second batch", err)
					case <-deadline.C:
						t.Fatal("second batch did not start")
					case <-time.After(time.Millisecond):
					}
				}
				firstOutput := out.snapshot()
				if asJSON {
					var batch suggest.Report
					if err := json.Unmarshal([]byte(firstOutput), &batch); err != nil || len(batch.Items) != 25 || batch.Processed != 25 || batch.Total != 26 {
						t.Fatalf("first output=%s err=%v", firstOutput, err)
					}
				} else if strings.Count(firstOutput, "Track ") != 25 {
					t.Fatal("first batch was buffered", firstOutput)
				}
				if outcome == "cancelled" {
					cancel()
				} else {
					release()
				}
				err = <-done
				if (err == nil) != (outcome == "success") {
					t.Fatal(outcome, err)
				}
				output := out.snapshot()
				want := 25
				if outcome == "success" {
					want = 26
				}
				if asJSON {
					dec := json.NewDecoder(strings.NewReader(output))
					count, records := 0, 0
					for {
						var batch suggest.Report
						err := dec.Decode(&batch)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						records++
						count += len(batch.Items)
						if batch.Processed != count || batch.Total != 26 {
							t.Fatal(batch)
						}
					}
					wantRecords := 1
					if outcome == "success" {
						wantRecords = 2
					}
					if count != want || records != wantRecords {
						t.Fatalf("duplicate or missing output: %s", output)
					}
				} else if strings.Count(output, "Track ") != want {
					t.Fatal("duplicate or missing text results", output)
				}
			})
		}
	}
}
