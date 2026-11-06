package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestAPIWatchPollingPreservesRetainedJobs(t *testing.T) {
	s, _ := testAPI(t)
	retained := runAPIJob(t, s, "intake.watch.list", `{}`)
	var initial intake.WatchState
	if err := json.Unmarshal(mustJSON(t, retained["result"]), &initial); err != nil {
		t.Fatal(err)
	}
	added := runAPIJob(t, s, "intake.watch.add", string(mustJSON(t, map[string]any{
		"path": t.TempDir(), "revision": initial.Revision,
	})))
	if added["state"] != "succeeded" {
		t.Fatalf("watch configuration failed: %+v", added)
	}
	var configured intake.WatchState
	if err := json.Unmarshal(mustJSON(t, added["result"]), &configured); err != nil {
		t.Fatal(err)
	}
	if len(configured.Roots) != 1 {
		t.Fatalf("watch add returned no configured root: %+v", configured)
	}
	libraries := requestAPI(t, s, http.MethodGet, "/v1/libraries", "")
	var opened []openedLibrary
	if err := json.Unmarshal(libraries.Body.Bytes(), &opened); err != nil || libraries.Code != http.StatusOK || len(opened) != 1 {
		t.Fatalf("configured library missing: HTTP %d %s error=%v", libraries.Code, libraries.Body.String(), err)
	}
	s.mu.Lock()
	before := len(s.jobs)
	s.mu.Unlock()
	for i := range maxJobs + 1 {
		path := "/v1/intake/watch"
		if i%2 == 1 {
			path = opened[0].URL + "/intake/watch"
		}
		response := requestAPI(t, s, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("watch read returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var state intake.WatchState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.Revision != configured.Revision || len(state.Roots) != 1 || state.Roots[0].ID != configured.Roots[0].ID {
			t.Fatalf("watch polling lost durable configuration: %+v", state)
		}
	}
	s.mu.Lock()
	after := len(s.jobs)
	s.mu.Unlock()
	if after != before {
		t.Fatalf("watch polling created jobs: before=%d after=%d", before, after)
	}
	response := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+retained["id"].(string), "")
	if response.Code != http.StatusOK {
		t.Fatalf("watch polling evicted the retained result: HTTP %d", response.Code)
	}
}

func TestAPIWatchJobsKeepLibraryScope(t *testing.T) {
	first, _ := testAPI(t)
	second, _ := testAPI(t)
	read := func(s *Server, operation string, params any) intake.WatchState {
		t.Helper()
		job := runAPIJob(t, s, operation, string(mustJSON(t, params)))
		if job["state"] != "succeeded" || (job["history_id"] != nil && job["history_id"] != "") {
			t.Fatalf("watch job created collection history or failed: %+v", job)
		}
		var state intake.WatchState
		if err := json.Unmarshal(mustJSON(t, job["result"]), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	initial := read(first, "intake.watch.list", map[string]any{})
	added := read(first, "intake.watch.add", map[string]any{"path": t.TempDir(), "revision": initial.Revision})
	if len(added.Roots) != 1 || !added.Roots[0].Recursive {
		t.Fatalf("HTTP add lost root/default: %+v", added)
	}
	other := read(second, "intake.watch.list", map[string]any{})
	if len(other.Roots) != 0 || other.Revision != initial.Revision {
		t.Fatalf("first library authorization leaked into second: %+v", other)
	}
	listed := read(first, "intake.watch.list", map[string]any{})
	if len(listed.Roots) != 1 || listed.Roots[0].ID != added.Roots[0].ID || listed.Revision != added.Revision {
		t.Fatalf("HTTP listing lost durable root: %+v", listed)
	}
}

func TestAPIWatchGETKeepsOpenedLibraryScope(t *testing.T) {
	s, _ := testAPI(t)
	initial := runAPIJob(t, s, "intake.watch.list", `{}`)
	var state intake.WatchState
	if err := json.Unmarshal(mustJSON(t, initial["result"]), &state); err != nil {
		t.Fatal(err)
	}
	added := runAPIJob(t, s, "intake.watch.add", string(mustJSON(t, map[string]any{
		"path": t.TempDir(), "revision": state.Revision,
	})))
	if added["state"] != "succeeded" {
		t.Fatalf("watch configuration failed: %+v", added)
	}
	other, path := testAPI(t)
	other.Close()
	response := requestAPI(t, s, http.MethodPost, "/v1/libraries", string(mustJSON(t, map[string]any{"paths": []string{path}})))
	var opened []openedLibrary
	if err := json.Unmarshal(response.Body.Bytes(), &opened); err != nil || response.Code != http.StatusOK || len(opened) != 1 {
		t.Fatalf("second library did not open: HTTP %d %s error=%v", response.Code, response.Body.String(), err)
	}
	for _, tt := range []struct {
		path  string
		roots int
	}{{"/v1/intake/watch", 1}, {opened[0].URL + "/intake/watch", 0}, {"/v1/intake/watch", 1}} {
		response := requestAPI(t, s, http.MethodGet, tt.path, "")
		var state intake.WatchState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || response.Code != http.StatusOK {
			t.Fatalf("watch read failed: HTTP %d %s error=%v", response.Code, response.Body.String(), err)
		}
		if len(state.Roots) != tt.roots {
			t.Fatalf("watch roots crossed opened library scope: path=%s roots=%+v want=%d", tt.path, state.Roots, tt.roots)
		}
	}
}

func TestAPIWatchRevisionAndRecursiveDefaults(t *testing.T) {
	for _, recursive := range []bool{true, false} {
		name := "default_recursive"
		if !recursive {
			name = "explicit_nonrecursive"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := testAPI(t)
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				return &systemcheck.Report{Checks: map[string]systemcheck.Result{
					systemcheck.EngineDesktop: {Status: "ok", Running: new(true)},
				}}, nil
			}
			run := func(operation string, params any) intake.WatchState {
				t.Helper()
				value, historyID, err := s.RunOperation(t.Context(), operation, mustJSON(t, params))
				if err != nil || historyID != "" {
					t.Fatalf("%s: result=%+v history=%q error=%v", operation, value, historyID, err)
				}
				state, ok := value.(intake.WatchState)
				if !ok {
					t.Fatalf("%s returned %T", operation, value)
				}
				return state
			}
			initial := run("intake.watch.list", map[string]any{})
			if len(initial.Roots) != 0 {
				t.Fatalf("new library already has watch authorizations: %+v", initial)
			}
			params := map[string]any{"path": t.TempDir(), "revision": initial.Revision}
			if !recursive {
				params["recursive"] = false
			}
			added := run("intake.watch.add", params)
			if len(added.Roots) != 1 || added.Roots[0].Recursive != recursive || !added.Roots[0].Enabled || added.Revision <= initial.Revision {
				t.Fatalf("add did not retain requested authorization: %+v", added)
			}
			id := added.Roots[0].ID
			stale := map[string]any{"id": id, "revision": initial.Revision}
			_, historyID, err := s.RunOperation(t.Context(), "intake.watch.disable", mustJSON(t, stale))
			if err == nil || progress.ErrorCode(err) != "watch_revision_changed" || historyID != "" {
				t.Fatalf("stale config accepted: history=%q error=%v", historyID, err)
			}
			unchanged := run("intake.watch.list", map[string]any{})
			if unchanged.Revision != added.Revision || !unchanged.Roots[0].Enabled {
				t.Fatalf("stale mutation changed authorization: %+v", unchanged)
			}
			for _, action := range []string{"disable", "enable", "remove"} {
				added = run("intake.watch."+action, map[string]any{"id": id, "revision": added.Revision})
				if action == "remove" {
					if len(added.Roots) != 0 {
						t.Fatalf("removed authorization remains visible: %+v", added)
					}
				} else if len(added.Roots) != 1 || added.Roots[0].Enabled != (action == "enable") {
					t.Fatalf("%s did not change authorization: %+v", action, added)
				}
			}
			data, err := json.Marshal(added)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(data, &wire); err != nil || string(wire["reason"]) != `""` || string(wire["roots"]) != "[]" {
				t.Fatalf("normal state lost required wire fields: %s %v", data, err)
			}
		})
	}
}

func TestAPIWatchRejectsMalformedRequestsBeforeExecution(t *testing.T) {
	s, _ := testAPI(t)
	for _, request := range []struct {
		operation string
		params    string
	}{
		{"add", `{"path":"/unused"}`},
		{"add", `{"revision":0}`},
		{"add", `{"path":"/unused","revision":0.5}`},
		{"add", `{"path":"/unused","revision":0,"recursive":"false"}`},
		{"enable", `{"revision":0}`},
		{"disable", `{"id":"opaque"}`},
		{"remove", `{"id":"opaque","revision":null}`},
		{"activate", `{}`},
		{"reconcile", `{"revision":0}`},
		{"list", `{"library":"other"}`},
	} {
		t.Run(request.operation+request.params, func(t *testing.T) {
			_, historyID, err := s.RunOperation(t.Context(), "intake.watch."+request.operation, []byte(request.params))
			if err == nil || historyID != "" {
				t.Fatalf("malformed request executed: history=%q error=%v", historyID, err)
			}
		})
	}
}
