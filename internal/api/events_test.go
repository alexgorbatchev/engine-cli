package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requestAPIWithCursor(t *testing.T, s http.Handler, id, cursor string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id+"/events", nil)
	r.Host = "localhost"
	r.Header.Set("Last-Event-ID", cursor)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestAPILiveEventsDisconnectDoesNotCancelJob(t *testing.T) {
	s, _ := testAPI(t)
	started := make(chan struct{})
	s.operations["test.live"] = define("test.live", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	id := submitAPIJob(t, s, "test.live", `{}`)
	waitSignal(t, started)
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/jobs/"+id+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	observed := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			observed = true
			break
		}
	}
	if !observed {
		t.Fatalf("live stream ended before progress: %v", scanner.Err())
	}
	cancel()
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.State != "running" {
		t.Fatalf("disconnect canceled job: %+v", job)
	}
	requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
	if job := awaitAPIJob(t, s, id); job["state"] != "cancelled" {
		t.Fatalf("explicit cancellation failed=%+v", job)
	}
}

func TestAPIEventsReplayAndResume(t *testing.T) {
	s, _ := testAPI(t)
	job := runAPIJob(t, s, "playlist.list", `{}`)
	id := job["id"].(string)
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id+"/events", "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events status=%d type=%s body=%s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	var sequence int
	lastPercent := -1.0
	count := 0
	complete := false
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			JobID    string  `json:"job_id"`
			Sequence int     `json:"sequence"`
			Percent  float64 `json:"percent"`
			Type     string  `json:"type"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			t.Fatal(err)
		}
		if event.JobID != id || event.Sequence <= sequence || event.Percent < lastPercent || event.Percent < 0 || event.Percent > 100 {
			t.Fatalf("invalid ordered event: %+v after sequence %d percent %.2f", event, sequence, lastPercent)
		}
		sequence, lastPercent = event.Sequence, event.Percent
		complete = event.Type == "complete"
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 || !complete || lastPercent != 100 {
		t.Fatalf("terminal replay count=%d complete=%v percent=%.2f body=%s", count, complete, lastPercent, w.Body)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id+"/events", nil)
	r.Host = "localhost"
	r.Header.Set("Last-Event-ID", strconv.Itoa(sequence))
	resumed := httptest.NewRecorder()
	s.ServeHTTP(resumed, r)
	if resumed.Code != http.StatusNoContent || strings.Contains(resumed.Body.String(), "data:") {
		t.Fatalf("repeated consumed terminal events: %d %s", resumed.Code, resumed.Body)
	}
}

func TestAPIBrowserOrigins(t *testing.T) {
	initial, lib := testAPI(t)
	initial.Close()
	s, err := New(Config{LibraryDir: lib, NoBackup: true, AllowedOrigins: []string{"http://localhost:3000"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tt := range []struct {
		origin  string
		allowed bool
	}{{"http://localhost:3000", true}, {"https://untrusted.example", false}} {
		r := httptest.NewRequest(http.MethodGet, "/v1/operations", nil)
		r.Host = "localhost"
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if tt.allowed {
			if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != tt.origin {
				t.Fatalf("allowed origin status=%d headers=%v", w.Code, w.Header())
			}
		} else if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("untrusted browser accepted: %d %s", w.Code, w.Body)
		}
	}
}
