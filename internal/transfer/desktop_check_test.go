package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/engine-cli/internal/testdesktop"
)

// Applying a sync observes Engine DJ Desktop only through the caller's check,
// never through the machine, and refuses without one.
func TestApplyObservesDesktopOnlyThroughTheCallersCheck(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Same','song.mp3','song.mp3')`)
	if err := os.WriteFile(filepath.Join(source, "song.mp3"), []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(t.Context(), o)
	if err != nil || !preview.Applicable {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	o.DryRun, o.PlanToken = false, preview.PlanToken
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
			o.CheckMutation = tc.check
			var refusal *systemcheck.Error
			if r, err := Run(t.Context(), o); !errors.As(err, &refusal) || refusal.Code != tc.code || r != nil {
				t.Fatalf("report=%+v err=%v, want %s", r, err, tc.code)
			}
			var tracks int
			if err := d.QueryRow("SELECT count(*) FROM Track").Scan(&tracks); err != nil || tracks != 0 {
				t.Fatalf("refused sync changed the destination: tracks=%d %v", tracks, err)
			}
			if _, err := os.Stat(filepath.Join(dest, ".engine-cli", "history")); !os.IsNotExist(err) {
				t.Fatalf("refused sync wrote history: %v", err)
			}
		})
	}
	if calls != 1 {
		t.Fatalf("running check consulted %d times, want 1", calls)
	}
	o.CheckMutation = func(ctx context.Context) error { calls++; return testdesktop.AllowClosed(ctx) }
	applied, err := Run(t.Context(), o)
	if err != nil || applied.OperationID == "" || calls != 2 {
		t.Fatalf("closed check: %+v %v calls=%d", applied, err, calls)
	}
}
