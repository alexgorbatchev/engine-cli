// Package testdesktop gives tests deterministic Engine DJ Desktop observations,
// so no test depends on whether Desktop runs on the machine.
package testdesktop

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// Report is a successful Engine DJ Desktop observation.
func Report(running bool) *systemcheck.Report {
	return &systemcheck.Report{Checks: map[string]systemcheck.Result{
		systemcheck.EngineDesktop: {Status: "ok", Running: &running, PIDs: []int{}},
	}}
}

// Closed observes Engine DJ Desktop closed. It replaces systemcheck.Run.
func Closed(context.Context, []string) (*systemcheck.Report, error) {
	return Report(false), nil
}

// AllowClosed is a mutation check that observes Engine DJ Desktop closed.
func AllowClosed(context.Context) error {
	return systemcheck.AllowMutation(Report(false))
}
