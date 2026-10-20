package api

import (
	"context"
	"errors"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false), PIDs: []int{}}}}, nil
	}
	os.Exit(m.Run())
}

func TestCheckErrorsAndPreview(t *testing.T) {
	s, _ := testAPI(t)
	for _, path := range []string{"/v1/system/check?checks=bad", "/v1/system/check?unknown=x"} {
		w := requestAPI(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code)
		}
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) { return nil, errors.New("unavailable") }
	w := requestAPI(t, s, http.MethodGet, "/v1/system/check", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code)
	}
	op := s.operations["playlist.create"]
	bound, err := op.bind([]byte(`{"name":"Blocked"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.execute(context.Background(), op, bound); err == nil {
		t.Fatal("allowed unknown status")
	}
	called := false
	bound = &boundOperation{preview: true, run: func(context.Context) (any, error) { called = true; return nil, nil }}
	if _, _, err := s.execute(context.Background(), op, bound); err != nil || !called {
		t.Fatalf("preview: %v", err)
	}
	s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	id := submitAPIJob(t, s, "playlist.create", `{"name":"Blocked"}`)
	job := awaitAPIJob(t, s, id)
	if job["error"].(map[string]any)["code"] != "engine_desktop_running" {
		t.Fatal(job)
	}
}

func TestSystemCheckAndGuard(t *testing.T) {
	s, lib := testAPI(t)
	r := runAPIJob(t, s, "system.check", `{"checks":["engine_desktop"]}`)
	if r["history_id"] != nil {
		t.Fatal(r)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/system/check?checks=engine_desktop", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	original := querySystem
	t.Cleanup(func() { querySystem = original })
	querySystem = func(context.Context, []string) (*systemcheck.Report, error) {
		return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(true)}}}, nil
	}
	op := s.operations["playlist.create"]
	bound, err := op.bind([]byte(`{"name":"Blocked"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, id, err := s.execute(context.Background(), op, bound)
	if err == nil || id != "" {
		t.Fatalf("id=%s err=%v", id, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("wrote history or backup: %v", err)
	}
}
