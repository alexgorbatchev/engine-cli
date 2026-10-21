package systemcheck

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCheckDesktop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		processes []process
		err       error
		status    string
		running   *bool
		pids      []int
	}{
		{"absent", []process{{1, "engine-cli"}, {2, "Engine DJ Helper"}}, nil, "ok", new(false), []int{}},
		{"running", []process{{9, "Engine DJ"}, {3, "engine dj.exe"}, {4, "Engine DJ.exe.bak"}}, nil, "ok", new(true), []int{3, 9}},
		{"failure", nil, errors.New("denied"), "error", nil, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r, err := run(context.Background(), []string{"engine_desktop", "engine_desktop"}, func(context.Context) ([]process, error) { calls++; return tc.processes, tc.err })
			if err != nil {
				t.Fatal(err)
			}
			got := r.Checks["engine_desktop"]
			if calls != 1 || len(r.Checks) != 1 || r.CheckedAt.IsZero() || r.Platform == "" || got.Status != tc.status || !reflect.DeepEqual(got.Running, tc.running) || !reflect.DeepEqual(got.PIDs, tc.pids) {
				t.Fatalf("report=%+v check=%+v calls=%d", r, got, calls)
			}
			if (got.Error != nil) != (tc.err != nil) {
				t.Fatalf("error=%+v", got.Error)
			}
		})
	}
}

func TestValidationAndCancellation(t *testing.T) {
	if err := Validate([]string{"missing"}); err == nil {
		t.Fatal("accepted unknown check")
	}
	if err := Validate(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), []string{"missing"}, nil); err == nil {
		t.Fatal("accepted unknown check")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := run(context.Background(), nil, func(context.Context) ([]process, error) { return nil, context.Canceled }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	r, err := run(context.Background(), nil, func(context.Context) ([]process, error) { return nil, nil })
	if err != nil || len(r.Checks) != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}

func TestMutationDecision(t *testing.T) {
	for _, tc := range []struct {
		r    *Report
		code string
	}{
		{nil, "engine_desktop_unknown"},
		{&Report{}, "engine_desktop_unknown"},
		{&Report{Checks: map[string]Result{EngineDesktop: {Status: "error"}}}, "engine_desktop_unknown"},
		{&Report{Checks: map[string]Result{EngineDesktop: {Status: "ok", Running: new(true)}}}, "engine_desktop_running"},
		{&Report{Checks: map[string]Result{EngineDesktop: {Status: "ok", Running: new(false)}}}, ""},
	} {
		err := AllowMutation(tc.r)
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code || e.Error() == "" {
			t.Fatalf("%v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := run(ctx, nil, func(context.Context) ([]process, error) { cancel(); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
