package history

import (
	"context"
	"errors"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/testdesktop"
)

// Smartlist repair and its recovery observe Engine DJ Desktop only through the
// caller's check, never through the machine, and refuse without one.
func TestSmartlistRepairObservesDesktopOnlyThroughTheCallersCheck(t *testing.T) {
	ctx := t.Context()
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil || review.Reconnected != 1 {
		t.Fatalf("review: %+v %v", review, err)
	}
	calls := 0
	for _, tc := range []struct {
		name  string
		check func(context.Context) error
		code  string
	}{
		{"missing", nil, "engine_desktop_unknown"},
		{"running", func(context.Context) error { calls++; return systemcheck.AllowMutation(testdesktop.Report(true)) }, "engine_desktop_running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refusal *systemcheck.Error
			if result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken, tc.check); !errors.As(err, &refusal) || refusal.Code != tc.code || result != nil {
				t.Fatalf("apply: %+v %v, want %s", result, err, tc.code)
			}
			if result, err := RecoverSmartlistRepair(ctx, lib, tc.check); !errors.As(err, &refusal) || refusal.Code != tc.code || result != nil {
				t.Fatalf("recover: %+v %v, want %s", result, err, tc.code)
			}
			again, err := PreviewSmartlistRepair(ctx, lib)
			if err != nil || again.PlanToken != review.PlanToken {
				t.Fatalf("refused repair changed the library: %+v %v", again, err)
			}
		})
	}
	if calls != 2 {
		t.Fatalf("running check consulted %d times, want 2", calls)
	}
	closed := func(ctx context.Context) error { calls++; return testdesktop.AllowClosed(ctx) }
	result, err := ApplySmartlistRepair(ctx, lib, review.PlanToken, closed)
	if err != nil || result == nil || !result.Applied || calls != 3 {
		t.Fatalf("closed check: %+v %v calls=%d", result, err, calls)
	}
}
