package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
)

func sharedDependencyAPI(t *testing.T, mutate func(context.Context, string, []string) ([]byte, error)) *Server {
	t.Helper()
	s, _ := testAPI(t)
	s.config.ToolRunner = func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"--version"}) {
			return []byte("9.0.0"), nil
		}
		if slices.Equal(args, []string{"--json", "dependencies"}) {
			return []byte(`{"status":"ok","dependencies":[{"name":"shared","installed":true,"detectedVersion":"1.0","minVersion":"2.0","satisfied":false}],"changed":[]}`), nil
		}
		return mutate(ctx, tool, args)
	}
	for _, op := range s.toolOperations() {
		s.operations[op.Name] = op
	}
	return s
}

func TestSharedDependencyInspectionAndRequestBoundary(t *testing.T) {
	s := sharedDependencyAPI(t, func(context.Context, string, []string) ([]byte, error) {
		t.Error("inspection or invalid request invoked mutation")
		return nil, errors.New("unexpected mutation")
	})
	result, _, err := s.RunOperation(t.Context(), "tools.inspect", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	report := result.(*acquire.ToolsInspection)
	if !report.Complete || len(report.Tools) != 4 || len(report.Dependencies) != 5 {
		t.Fatalf("inspection %+v", report)
	}
	for _, dep := range report.Dependencies {
		if dep.Name == "shared" && (len(dep.RequiredBy) != 3 || dep.MinimumVersion != "2.0" || dep.Satisfied || !dep.Verified) {
			t.Fatalf("shared evidence %+v", dep)
		}
	}
	for _, operation := range []string{"tools.dependencies.install", "tools.dependencies.update"} {
		for _, body := range []string{`{}`, `{"dependencies":[]}`, `{"dependencies":[""]}`, `{"dependencies":["shared"],"tool":"fetch-track"}`} {
			response := requestAPI(t, s, http.MethodPost, "/v1/operations/"+operation, body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted %s: %d %s", operation, body, response.Code, response.Body)
			}
		}
	}
}

func TestSharedDependencyJobRetainsReceiptsAfterFailureOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancelled"}[cancelled], func(t *testing.T) {
			started := make(chan struct{})
			calls := 0
			s := sharedDependencyAPI(t, func(ctx context.Context, tool string, args []string) ([]byte, error) {
				calls++
				if !slices.Equal(args, []string{"--json", "dependencies", "update", "shared"}) {
					t.Errorf("unexpected mutation %s %v", tool, args)
				}
				if calls == 2 {
					close(started)
					if cancelled {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					return nil, errors.New("installation refused")
				}
				return []byte(`{"status":"ok","dependencies":[],"changed":["shared"]}`), nil
			})
			id := submitAPIJob(t, s, "tools.dependencies.update", `{"dependencies":["shared"]}`)
			waitSignal(t, started)
			if cancelled {
				response := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
				if response.Code != http.StatusAccepted {
					t.Fatalf("cancel=%d %s", response.Code, response.Body)
				}
			}
			job := awaitAPIJob(t, s, id)
			wantState := map[bool]string{false: "failed", true: "cancelled"}[cancelled]
			if job["state"] != wantState {
				t.Fatalf("state=%v want=%s", job, wantState)
			}
			result := job["result"].(map[string]any)
			receipts := result["receipts"].([]any)
			wantCount := map[bool]int{false: 3, true: 2}[cancelled]
			if result["status"] != "partial" || len(receipts) != wantCount || receipts[0].(map[string]any)["status"] != "success" || receipts[1].(map[string]any)["status"] != "error" {
				t.Fatalf("completed receipts lost: %+v", result)
			}
		})
	}
}
