package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
)

func TestSuggestionLiveResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	for _, outcome := range []string{"success", "failed", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = d.Exec(`WITH RECURSIVE n(id) AS (SELECT 2 UNION ALL SELECT id+1 FROM n WHERE id<26) INSERT INTO Track(id,title,artist) SELECT id,'Song','Artist' FROM n`)
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
					items = append(items, map[string]any{"id": id, "title": "Song", "artist": "Artist", "reason": "Cleanup"})
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
cd "$ENGINE_SUGGEST_FIXTURE"
if [ ! -f first ]; then
 : > first
 cat 1
 exit
fi
trap 'exit 130' INT
: > waiting
while [ ! -f release ]; do sleep 0.01; done
if [ "$ENGINE_SUGGEST_OUTCOME" = failed ]; then
 echo '{"error":{"code":"harness_failed","message":"Second batch failed"}}' >&2
 exit 1
fi
cat 26
`
			if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ENGINE_SUGGEST_FIXTURE", dir)
			t.Setenv("ENGINE_SUGGEST_OUTCOME", outcome)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			release := func() {
				if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
					t.Error(err)
				}
			}
			t.Cleanup(release)
			body, err := json.Marshal(map[string]any{"tracks": ids, "harness": "codex"})
			if err != nil {
				t.Fatal(err)
			}
			id := submitAPIJob(t, s, "track.suggest", string(body))
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			for {
				if _, err := os.Stat(filepath.Join(dir, "waiting")); err == nil {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("second batch did not start")
				case <-time.After(time.Millisecond):
				}
			}
			status := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
			var running Job
			if err := json.Unmarshal(status.Body.Bytes(), &running); err != nil {
				t.Fatal(err)
			}
			runningResult := running.Result.(map[string]any)
			if running.State != "running" || len(runningResult["items"].([]any)) != 25 || runningResult["processed"] != float64(25) || runningResult["total"] != float64(26) {
				t.Fatalf("running job=%s", status.Body)
			}

			// Read the live SSE connection before releasing the blocked second call.
			server := httptest.NewServer(s)
			defer server.Close()
			streamCtx, cancelStream := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancelStream()
			req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/v1/jobs/"+id+"/events", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			var events []progress.Event
			readEvent := func() bool {
				for scanner.Scan() {
					if !strings.HasPrefix(scanner.Text(), "data: ") {
						continue
					}
					var event progress.Event
					if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					events = append(events, event)
					return true
				}
				return false
			}
			for {
				if !readEvent() {
					t.Fatal("no live result event", scanner.Err())
				}
				if events[len(events)-1].Type == "result" {
					break
				}
			}
			var first suggest.Report
			if err := json.Unmarshal(events[len(events)-1].Result, &first); err != nil {
				t.Fatal(err)
			}
			if len(first.Items) != 25 || first.Processed != 25 || first.Total != 26 {
				t.Fatalf("first=%+v", first)
			}
			if outcome == "cancelled" {
				requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
			} else {
				release()
			}
			final := awaitAPIJob(t, s, id)
			for readEvent() {
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			wantState, wantCount := "succeeded", 26
			if outcome != "success" {
				wantState = outcome
				wantCount = 25
			}
			result := final["result"].(map[string]any)
			if final["state"] != wantState || len(result["items"].([]any)) != wantCount || result["processed"] != float64(wantCount) || result["total"] != float64(26) {
				t.Fatalf("final=%+v", final)
			}
			count := 0
			lastPercent := 0.0
			for i, event := range events {
				if event.Sequence != uint64(i+1) || event.Percent < lastPercent {
					t.Fatalf("unordered events: %+v", events)
				}
				lastPercent = event.Percent
				if event.Type == "result" {
					var batch suggest.Report
					if err := json.Unmarshal(event.Result, &batch); err != nil {
						t.Fatal(err)
					}
					count += len(batch.Items)
				}
			}
			if count != wantCount {
				t.Fatalf("duplicated or missing SSE items: %d", count)
			}
			last := events[len(events)-1]
			if outcome == "success" {
				if last.Type != "complete" || last.Percent != 100 {
					t.Fatal(last)
				}
			} else {
				code := "harness_failed"
				if outcome == "cancelled" {
					code = "cancelled"
				}
				if last.ErrorCode != code || last.Percent >= 100 || final["error"].(map[string]any)["code"] != code {
					t.Fatal(last, final)
				}
			}
		})
	}
}

func TestSuggestionSnapshotsStayImmutable(t *testing.T) {
	first := accumulateSuggestions(nil, json.RawMessage(`{"items":[{"id":1}],"processed":1,"total":2}`)).(*suggest.Report)
	next := accumulateSuggestions(first, json.RawMessage(`{"items":[{"id":2}],"processed":2,"total":2}`)).(*suggest.Report)
	if len(first.Items) != 1 || first.Processed != 1 || len(next.Items) != 2 || next.Items[0].ID != 1 || next.Items[1].ID != 2 || next.Processed != 2 {
		t.Fatal(first, next)
	}
	if got := accumulateSuggestions(next, json.RawMessage(`broken`)); got != next {
		t.Fatal("invalid event discarded completed results")
	}
}

func TestSuggestionResultsSurviveEventExpiryAndFailure(t *testing.T) {
	s, _ := testAPI(t)
	total := maxEvents + 2
	op := define("test.results", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		for id := 1; id <= total; id++ {
			data, err := json.Marshal(suggest.Report{Items: []suggest.Item{{ID: int64(id)}}, Processed: id, Total: total})
			if err != nil {
				return nil, err
			}
			progress.Emit(ctx, progress.Event{Type: "result", Result: data, Percent: 95 * float64(id) / float64(total)})
		}
		return nil, errors.New("failed after publishing results")
	})
	op.reduceResult = accumulateSuggestions
	s.operations[op.Name] = op
	id := submitAPIJob(t, s, op.Name, `{}`)
	job := awaitAPIJob(t, s, id)
	result := job["result"].(map[string]any)
	if job["state"] != "failed" || len(result["items"].([]any)) != total || result["processed"] != float64(total) {
		t.Fatal(job)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id+"/events", nil)
	req.Host = "localhost"
	req.Header.Set("Last-Event-ID", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "expired_cursor") {
		t.Fatal(w.Code, w.Body)
	}
}
