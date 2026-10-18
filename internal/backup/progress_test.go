package backup

import (
	"context"
	"testing"

	"github.com/dj/engine-cli/internal/progress"
)

func TestAutomaticBackupProgress(t *testing.T) {
	lib := createDummyEngineLibrary(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var events []progress.Event
	ctx := progress.WithReporter(context.Background(), func(e progress.Event) { events = append(events, e) })
	if _, err := AutoBackupIfNeeded(ctx, lib, false); err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("missing backup progress: %+v", events)
	}
	for _, e := range events {
		if e.Percent > 10 {
			t.Fatalf("backup consumed work percentage: %+v", e)
		}
	}
	if events[len(events)-1].Percent != 10 {
		t.Fatalf("backup not complete: %+v", events)
	}
}
