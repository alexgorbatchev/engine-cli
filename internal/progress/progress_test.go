package progress

import (
	"context"
	"testing"
)

func TestProgressMonotonic(t *testing.T) {
	var got []Event
	ctx := WithReporter(context.Background(), func(e Event) { got = append(got, e) })
	Emit(ctx, Event{Phase: "start", Percent: 0})
	Emit(ctx, Event{Phase: "work", Percent: 50})
	Emit(ctx, Event{Phase: "retry", Percent: 20})
	Emit(ctx, Event{Type: "complete", Phase: "done", Percent: 100})
	for i, want := range []float64{0, 50, 50, 100} {
		if got[i].Percent != want {
			t.Fatalf("event %d: %+v", i, got[i])
		}
	}
	Emit(context.Background(), Event{Percent: 10})
}
