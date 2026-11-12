package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func observedJob(t *testing.T, s *Server, id string) Job {
	t.Helper()
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
	var view Job
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestAPIJobDetailsLifecycle(t *testing.T) {
	s, _ := testAPI(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	s.operations["test.details"] = define("test.details", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
		close(entered)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	before := time.Now().UTC()
	w := requestAPI(t, s, http.MethodPost, "/v1/operations/test.details", `{}`)
	var accepted Job
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.State != "queued" || accepted.CreatedAt.Before(before) || accepted.StartedAt != nil || accepted.FinishedAt != nil || accepted.Parameters == nil {
		t.Fatalf("invalid accepted details: %+v", accepted)
	}
	waitSignal(t, entered)
	running := observedJob(t, s, accepted.ID)
	if running.StartedAt == nil || running.StartedAt.Before(running.CreatedAt) || running.FinishedAt != nil {
		t.Fatalf("invalid running details: %+v", running)
	}
	// A collection writer waits behind the active reader; cancelling it must
	// finish its age without pretending that execution ever started.
	queued := submitAPIJob(t, s, "playlist.create", `{"name":"Never created"}`)
	if view := observedJob(t, s, queued); view.StartedAt != nil || view.FinishedAt != nil {
		t.Fatalf("queued job has execution times: %+v", view)
	}
	requestAPI(t, s, http.MethodPost, "/v1/jobs/"+queued+"/cancel", "")
	awaitAPIJob(t, s, queued)
	cancelled := observedJob(t, s, queued)
	if cancelled.State != "cancelled" || cancelled.StartedAt != nil || cancelled.FinishedAt == nil || cancelled.FinishedAt.Before(cancelled.CreatedAt) {
		t.Fatalf("invalid queued cancellation times: %+v", cancelled)
	}
	release <- struct{}{}
	awaitAPIJob(t, s, accepted.ID)
	finished := observedJob(t, s, accepted.ID)
	if finished.FinishedAt == nil || finished.FinishedAt.Before(*finished.StartedAt) || !finished.CreatedAt.Equal(accepted.CreatedAt) || !finished.StartedAt.Equal(*running.StartedAt) {
		t.Fatalf("invalid final details: %+v", finished)
	}
	if repeated := observedJob(t, s, accepted.ID); !repeated.FinishedAt.Equal(*finished.FinishedAt) {
		t.Fatal("observing a completed job changed its finish time")
	}
}

func TestAPIJobParameters(t *testing.T) {
	type detailsParams struct {
		Name    string         `json:"name"`
		Enabled bool           `json:"enabled"`
		Count   int            `json:"count"`
		Nested  map[string]any `json:"nested"`
		Token   string         `json:"selection_token"`
		URL     string         `json:"candidate_url"`
	}
	s, _ := testAPI(t)
	executed := make(chan detailsParams, 1)
	s.operations["test.parameters"] = define("test.parameters", false, nil, func(_ context.Context, p detailsParams) (any, error) {
		executed <- p
		return nil, nil
	})
	id := submitAPIJob(t, s, "test.parameters", `{"name":"Reviewed title","enabled":false,"count":0,"nested":{"items":[{"api_key":"hidden"}]},"selection_token":"capability","candidate_url":"https://user:password@example.test/audio?signature=private"}`)
	awaitAPIJob(t, s, id)
	view := observedJob(t, s, id)
	if string(view.Parameters["name"]) != `"Reviewed title"` || string(view.Parameters["enabled"]) != "false" || string(view.Parameters["count"]) != "0" || len(view.Parameters) != 3 {
		t.Fatalf("safe parameters lost or private inputs exposed: %+v", view.Parameters)
	}
	if !reflect.DeepEqual(view.ParametersOmitted, []string{"candidate_url", "nested", "selection_token"}) {
		t.Fatalf("missing suppression metadata: %+v", view.ParametersOmitted)
	}
	if actual := <-executed; actual.Token != "capability" || actual.Nested == nil || actual.URL == "" {
		t.Fatalf("observation filtering changed execution: %+v", actual)
	}
	if _, exists := view.Parameters["unsupplied"]; exists {
		t.Fatal("observation invented a default input")
	}
}

func TestJobParameterSafety(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		private bool
	}{
		{"password", "hidden", true},
		{"headers", map[string]any{"x-custom": "hidden"}, true},
		{"query", "https://example.test/song?key=hidden", true},
		{"query", "https://example.test/song#hidden", true},
		{"query", "https://user:hidden@example.test/song", true},
		{"query", "https://example.test/song", false},
		{"paths", []any{"/music/one.flac", "/music/two.flac"}, false},
		{"metadata", map[string]any{"title": "Song", "rating": float64(0)}, false},
		{"value", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := privateJobParameter(test.name, test.value); got != test.private {
				t.Fatalf("private=%v want %v", got, test.private)
			}
		})
	}
	op := operation{Parameters: map[string]string{"name": "string"}}
	values := map[string]json.RawMessage{"name": json.RawMessage(`"Title"`), "unlisted": json.RawMessage(`"hidden"`)}
	parameters, omitted := jobParameters(op, values)
	values["name"][1] = 'X'
	if string(parameters["name"]) != `"Title"` || len(parameters) != 1 || len(omitted) != 0 {
		t.Fatalf("parameters not isolated or catalog restricted: %v %v", parameters, omitted)
	}
}
