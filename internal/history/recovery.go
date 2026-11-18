package history

import (
	"context"
	"fmt"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// RecoveryRequiredCode is the stable code of a change Engine refuses while an
// interrupted history entry awaits recovery, and of an interrupted operation
// whose entry stays pending.
const RecoveryRequiredCode = "history_recovery_required"

func recoveryRequired(id string) error {
	return &progress.Error{Code: RecoveryRequiredCode, Cause: fmt.Errorf("operation %s was interrupted; inspect it and use library undo %s --force to recover", id, id)}
}

// refuseWhileInterrupted holds every undo while an entry awaits recovery, as
// RunPrepared holds every other write. Recovering an entry restores its
// before-snapshot, so any change recorded after it would be silently reverted.
// The one undo it lets through names the newest interrupted entry alone: its
// review, or its forced recovery. An older one waits, because recovering the
// newer one afterwards would put back what the older recovery removed.
func refuseWhileInterrupted(all []Entry, targets []*Entry) error {
	// all is newest first, so the first pending entry is the one to recover.
	for i := range all {
		if all[i].After != nil {
			continue
		}
		if len(targets) == 1 && targets[0].ID == all[i].ID {
			return nil
		}
		return recoveryRequired(all[i].ID)
	}
	return nil
}

// recoveryTarget returns a copy of an interrupted entry whose After is the
// library as it is now, captured into a temporary directory, so a review plans
// its recovery exactly as the forced apply will without writing history.
func recoveryTarget(ctx context.Context, lib string, e *Entry) (*Entry, func(), error) {
	work, err := os.MkdirTemp("", "engine-recovery-review-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(work) }
	target := *e
	target.After, err = captureAfter(ctx, lib, target.Scope, work, target.Before)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("reading the library to review recovery of %s: %w", e.ID, err)
	}
	return &target, cleanup, nil
}
