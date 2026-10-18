// Package systemcheck provides read-only runtime facts for CLI and UI consumers.
package systemcheck

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
)

const EngineDesktop = "engine_desktop"

var errUnsupported = errors.New("Engine DJ Desktop detection is supported on macOS and Windows")

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Result struct {
	Status  string `json:"status"`
	Running *bool  `json:"running"`
	PIDs    []int  `json:"pids"`
	Error   *Error `json:"error,omitempty"`
}

type Report struct {
	CheckedAt time.Time         `json:"checked_at"`
	Platform  string            `json:"platform"`
	Checks    map[string]Result `json:"checks"`
}

type process struct {
	pid  int
	name string
}

func Validate(checks []string) error {
	for _, name := range checks {
		if name != EngineDesktop {
			return fmt.Errorf("unknown check %q; supported check: %s", name, EngineDesktop)
		}
	}
	return nil
}

func Run(ctx context.Context, checks []string) (*Report, error) {
	return run(ctx, checks, listProcesses)
}

func run(ctx context.Context, checks []string, snapshot func(context.Context) ([]process, error)) (*Report, error) {
	if err := Validate(checks); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	processes, err := snapshot(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	r := Result{Status: "ok", PIDs: []int{}}
	if err != nil {
		r.Status = "error"
		r.Error = &Error{Code: "process_query_failed", Message: "Could not check Engine DJ Desktop. Retry before changing a library."}
		if errors.Is(err, errUnsupported) {
			r.Status = "unsupported"
			r.Error = &Error{Code: "unsupported_platform", Message: errUnsupported.Error()}
		}
	} else {
		for _, p := range processes {
			if strings.EqualFold(p.name, "Engine DJ") || strings.EqualFold(p.name, "Engine DJ.exe") {
				r.PIDs = append(r.PIDs, p.pid)
			}
		}
		slices.Sort(r.PIDs)
		r.Running = new(len(r.PIDs) > 0)
	}
	return &Report{CheckedAt: time.Now().UTC(), Platform: runtime.GOOS, Checks: map[string]Result{EngineDesktop: r}}, nil
}

// AllowMutation checks a freshly obtained report, not a cached UI observation.
func AllowMutation(r *Report) error {
	if r == nil {
		return &Error{Code: "engine_desktop_unknown", Message: "Cannot confirm Engine DJ Desktop is closed; run system check and retry."}
	}
	c, ok := r.Checks[EngineDesktop]
	if !ok || c.Status != "ok" || c.Running == nil {
		return &Error{Code: "engine_desktop_unknown", Message: "Cannot confirm Engine DJ Desktop is closed; run system check and retry."}
	}
	if *c.Running {
		return &Error{Code: "engine_desktop_running", Message: "Engine DJ Desktop is running; close it before changing the library."}
	}
	return nil
}
