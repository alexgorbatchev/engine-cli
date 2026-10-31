package intake

import (
	"context"
	"testing"
	"time"
)

func TestWatchServiceUnchangedRegistrationDoesNotRescan(t *testing.T) {
	_, library := queueFixture(t)
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := service.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WatchReconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var last time.Time
	stable := false
	for !stable {
		state, err := service.WatchList(ctx)
		if err != nil || len(state.Roots) != 1 || state.Roots[0].LastScanAt == nil {
			t.Fatalf("registered root state: %+v %v", state, err)
		}
		current := *state.Roots[0].LastScanAt
		stable = current.Equal(last)
		last = current
		select {
		case <-ctx.Done():
			t.Fatal("unchanged registration never stopped scanning:", ctx.Err())
		case <-tick.C:
		}
	}
	// Native events and pending media are absent. The unchanged registration
	// must retain its periodic interval instead of scheduling its own next scan.
	observation := time.NewTimer(250 * time.Millisecond)
	defer observation.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-observation.C:
			return
		case <-tick.C:
			state, err := service.WatchList(ctx)
			if err != nil || len(state.Roots) != 1 || state.Roots[0].LastScanAt == nil || !state.Roots[0].LastScanAt.Equal(last) {
				t.Fatalf("unchanged registration scheduled another scan: %+v %v", state, err)
			}
		}
	}
}
