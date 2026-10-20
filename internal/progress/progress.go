// Package progress reports operation-owned progress to CLI and HTTP consumers.
package progress

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"
)

// Source retains the upstream meaning when a child operation is incorporated.
type Source struct {
	Name string `json:"name"`
	Event
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Event struct {
	JobID      string          `json:"job_id"`
	Sequence   uint64          `json:"sequence"`
	Timestamp  time.Time       `json:"timestamp"`
	Type       string          `json:"type"`
	Phase      string          `json:"phase,omitempty"`
	Subphase   string          `json:"subphase,omitempty"`
	Step       int             `json:"step,omitempty"`
	TotalSteps int             `json:"total_steps,omitempty"`
	Percent    float64         `json:"percent"`
	Message    string          `json:"message,omitempty"`
	Source     *Source         `json:"source,omitempty"`
	Candidate  json.RawMessage `json:"candidate,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
}

type reporterKey struct{}
type reporter struct {
	mu       sync.Mutex
	percent  float64
	report   func(Event)
	done     bool
	jobID    string
	sequence uint64
}

// WithReporter attaches a request-scoped, serialized progress observer.
// Callbacks must not reenter Emit on the same context.
func WithReporter(ctx context.Context, report func(Event)) context.Context {
	return WithJobReporter(ctx, "", report)
}

// WithJobReporter uses an explicit job identity, or generates one when empty.
func WithJobReporter(ctx context.Context, jobID string, report func(Event)) context.Context {
	if jobID == "" {
		jobID = rand.Text()
	}
	return context.WithValue(ctx, reporterKey{}, &reporter{report: report, jobID: jobID})
}

func Enabled(ctx context.Context) bool {
	r, ok := ctx.Value(reporterKey{}).(*reporter)
	return ok && r.report != nil
}

// Range incorporates a child operation into a portion of its parent's work.
func Range(ctx context.Context, start, end float64) context.Context {
	return WithReporter(ctx, func(e Event) {
		e.Percent = start + (end-start)*e.Percent/100
		if e.Type == "complete" {
			e.Type = "progress"
		}
		Emit(ctx, e)
	})
}

func Emit(ctx context.Context, e Event) {
	r, ok := ctx.Value(reporterKey{}).(*reporter)
	if !ok || r.report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	if math.IsNaN(e.Percent) || math.IsInf(e.Percent, 0) {
		e.Percent = r.percent
	}
	ceiling := 99.0
	if e.Type == "complete" {
		ceiling = 100
		e.Percent = 100
	}
	e.Percent = max(r.percent, min(ceiling, max(0, e.Percent)))
	r.percent = e.Percent
	r.sequence++
	e.JobID = r.jobID
	e.Sequence = r.sequence
	e.Timestamp = time.Now().UTC()
	if e.Type == "" {
		e.Type = "progress"
	}
	if e.Error != "" && e.ErrorCode == "" {
		e.ErrorCode = "operation_failed"
	}
	r.done = e.Type == "complete" || e.Type == "error" || e.Type == "cancelled"
	r.report(e)
}

// Stage expresses completed work, not an estimate of remaining wall time.
func Stage(ctx context.Context, phase string, percent float64) {
	Emit(ctx, Event{Type: "phase_start", Phase: phase, Percent: percent})
}

// Run owns terminal events. A successful return wins over late cancellation:
// cancellation cannot undo an operation which has already committed.
func Run(ctx context.Context, operation string, fn func(context.Context) error) error {
	Stage(ctx, operation, 0)
	err := ctx.Err()
	if err == nil {
		err = fn(ctx)
	}
	if err == nil {
		Emit(ctx, Event{Type: "complete", Phase: operation, Percent: 100})
	} else {
		kind := "error"
		if ctx.Err() != nil {
			kind = "cancelled"
		}
		Emit(ctx, Event{Type: kind, Phase: operation, Error: PublicError(err), ErrorCode: ErrorCode(err)})
	}
	return err
}

// PublicError keeps external runtime dumps out of machine and human progress.
func PublicError(err error) string {
	message, _, _ := strings.Cut(err.Error(), "\n")
	if strings.Contains(message, "panic:") || strings.Contains(message, "Traceback") {
		return "operation failed; run the dependent tool separately for details"
	}
	const maxErrorBytes = 1024
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes]
	}
	return message
}

// Item reports completed items in the work portion after the safety backup.
func Item(ctx context.Context, phase string, completed, total int) {
	percent := 10.0
	if total > 0 {
		percent += 85 * float64(completed) / float64(total)
	}
	Emit(ctx, Event{Phase: phase, Percent: percent})
}
