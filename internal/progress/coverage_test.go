package progress

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestRunTerminalCoverage(t *testing.T) {
	for _, kind := range []string{"complete", "error", "cancelled", "late cancellation"} {
		t.Run(kind, func(t *testing.T) {
			var events []Event
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = WithReporter(ctx, func(e Event) { events = append(events, e) })
			if kind == "cancelled" {
				cancel()
			}
			called := false
			err := Run(ctx, "operation", func(context.Context) error {
				called = true
				Stage(ctx, "work", 45)
				if kind == "error" {
					return errors.New("failed")
				}
				if kind == "late cancellation" {
					cancel()
				}
				return nil
			})
			terminal := events[len(events)-1]
			want := kind
			if kind == "late cancellation" {
				want = "complete"
			}
			if terminal.Type != want || (err != nil) != (want != "complete") {
				t.Fatalf("terminal=%+v err=%v", terminal, err)
			}
			if kind == "cancelled" && called {
				t.Fatal("cancelled operation executed")
			}
			if want == "complete" && terminal.Percent != 100 {
				t.Fatal(terminal)
			}
			if want != "complete" && terminal.Error == "" {
				t.Fatal(terminal)
			}
		})
	}
}

func TestPublicErrorAndTerminalIgnoringCoverage(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"action failed\nprivate traceback", "action failed"},
		{"panic: runtime failure", "operation failed; run the dependent tool separately for details"},
		{"Traceback (most recent call last)", "operation failed; run the dependent tool separately for details"},
		{strings.Repeat("x", 1100), strings.Repeat("x", 1024)},
	} {
		if got := PublicError(errors.New(tt.input)); got != tt.want {
			t.Fatalf("PublicError = %q want %q", got, tt.want)
		}
	}
	for _, terminal := range []string{"error", "cancelled", "complete"} {
		var events []Event
		ctx := WithReporter(context.Background(), func(e Event) { events = append(events, e) })
		Emit(ctx, Event{Type: terminal, Percent: 50})
		Emit(ctx, Event{Percent: 80})
		Emit(ctx, Event{Type: "complete", Percent: 100})
		if len(events) != 1 || events[0].Type != terminal {
			t.Fatalf("events after terminal: %+v", events)
		}
	}
}

func TestProgressMappingAndSanitization(t *testing.T) {
	var events []Event
	ctx := WithReporter(context.Background(), func(e Event) { events = append(events, e) })
	child := Range(ctx, 20, 60)
	Item(child, "items", 1, 2)
	Emit(child, Event{Percent: math.NaN()})
	Emit(child, Event{Percent: math.Inf(1)})
	Item(child, "unknown", 0, 0)
	Emit(child, Event{Percent: 200})
	for i, want := range []float64{41, 41, 41, 41, 59.6} {
		if events[i].Percent != want || events[i].Type == "" || events[i].Timestamp.IsZero() {
			t.Fatalf("event %d=%+v want %v", i, events[i], want)
		}
	}
	Emit(WithReporter(context.Background(), nil), Event{Percent: 10})
}

func TestProgressEnabledAndChildCompletion(t *testing.T) {
	if Enabled(context.Background()) || Enabled(WithReporter(context.Background(), nil)) {
		t.Fatal("reporting enabled without observer")
	}
	var events []Event
	ctx := WithReporter(context.Background(), func(e Event) { events = append(events, e) })
	if !Enabled(ctx) {
		t.Fatal("reporter disabled")
	}
	if err := Run(Range(ctx, 20, 60), "child", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != "progress" || last.Percent != 60 {
		t.Fatalf("child completion escaped: %+v", last)
	}
	Emit(ctx, Event{Type: "phase_start", Percent: 100})
	if events[len(events)-1].Percent >= 100 {
		t.Fatal("nonterminal event completed operation")
	}
	Emit(ctx, Event{Type: "complete", Percent: 100})
	if events[len(events)-1].Percent != 100 {
		t.Fatal("completion did not reach 100")
	}
}
