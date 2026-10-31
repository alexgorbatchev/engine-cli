package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// ApplySmartlistRepair owns its stronger rollback boundary; generic history.Run
// deliberately records partial failures and must not wrap this publication.
func ApplySmartlistRepair(ctx context.Context, lib, token string) (result *SmartlistRepairResult, err error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	lib = filepath.Dir(filepath.Dir(dir))
	review, err := PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		return nil, err
	}
	if token == "" || review.PlanToken != token {
		return nil, smartlistRepairStale()
	}
	if review.Reconnected+review.Removed == 0 {
		return &SmartlistRepairResult{SmartlistRepairReview: *review}, nil
	}
	if err := allowSmartlistRepair(ctx); err != nil {
		return nil, err
	}
	owner, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	return applySmartlistRepairLocked(ctx, lib, dir, token)
}

// The ordinary protected mutation pipeline may use this seam while already
// holding its history lock. It must perform its fresh Desktop check first.
func applySmartlistRepairLocked(ctx context.Context, lib, dir, token string) (*SmartlistRepairResult, error) {
	if err := checkSmartlistRepair(dir); err != nil {
		return nil, err
	}
	plan, err := planSmartlistRepair(ctx, lib)
	if err != nil {
		return nil, err
	}
	if token != "" && token != plan.review.PlanToken {
		return nil, smartlistRepairStale()
	}
	result := &SmartlistRepairResult{SmartlistRepairReview: plan.review}
	if len(plan.links.patches) == 0 {
		return result, nil
	}
	prepared, err := prepareSmartlistRepair(ctx, lib, dir, plan)
	if err != nil {
		return nil, err
	}
	current, err := planSmartlistRepair(ctx, lib)
	if err != nil {
		return nil, err
	}
	if current.review.PlanToken != plan.review.PlanToken {
		return nil, smartlistRepairStale()
	}
	if err := validateSmartlistTargets(ctx, lib, prepared.dir, prepared.journal, false); err != nil {
		return nil, err
	}
	if err := prepareSmartlistJournal(ctx, prepared.dir, prepared.journal); err != nil {
		return nil, err
	}
	if err := publishSmartlistTargets(ctx, lib, prepared); err != nil {
		// Cancellation cannot cancel rollback. A failed rollback retains intent
		// and all originals for explicit protected recovery on the next attempt.
		rollback := rollbackSmartlistRepair(context.WithoutCancel(ctx), lib, prepared.dir, prepared.journal)
		return nil, errors.Join(err, rollback)
	}
	result.Applied, result.UndoID = true, prepared.undoID
	return result, nil
}

func publishSmartlistTargets(ctx context.Context, lib string, prepared *smartlistPrepared) error {
	for _, image := range prepared.journal.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeSmartlistTarget(ctx, lib, prepared.dir, image, false); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return finishSmartlistJournal(prepared.dir, "committed.json")
}

// RecoverSmartlistRepair never rolls a partial repair forward. All original
// evidence and every allowed target state are validated before its first write.
func RecoverSmartlistRepair(ctx context.Context, lib string) (result *SmartlistRepairRecovery, err error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	lib = filepath.Dir(filepath.Dir(dir))
	if err := allowSmartlistRepair(ctx); err != nil {
		return nil, err
	}
	owner, err := lock(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	active := filepath.Join(filepath.Dir(dir), "smartlist-repair")
	if _, err := os.Lstat(filepath.Join(active, smartlistJournalName)); os.IsNotExist(err) {
		if err := checkSmartlistRepair(dir); err != nil {
			return nil, err
		}
		return &SmartlistRepairRecovery{}, nil
	} else if err != nil {
		return nil, err
	}
	journal, err := readSmartlistJournal(ctx, active)
	if err != nil {
		return nil, smartlistRepairPending(err)
	}
	inventory, _, err := readSmartlistVersion(ctx, filepath.Join(lib, "Database2", "m.db"), "")
	if err != nil {
		return nil, smartlistRepairPending(err)
	}
	if inventory.EngineUUID != journal.Library {
		return nil, smartlistRepairPending(errors.New("Smartlist repair belongs to a different native library"))
	}
	if err := rollbackSmartlistRepair(ctx, lib, active, *journal); err != nil {
		return nil, smartlistRepairPending(err)
	}
	return &SmartlistRepairRecovery{RolledBack: true, RestoredTargets: len(journal.Images)}, nil
}

func rollbackSmartlistRepair(ctx context.Context, lib, dir string, journal smartlistJournal) error {
	if err := restoreSmartlistTargets(ctx, lib, dir, journal); err != nil {
		return smartlistRepairPending(err)
	}
	if err := finishSmartlistJournal(dir, "rolled-back.json"); err != nil {
		return smartlistRepairPending(err)
	}
	return nil
}

func finishSmartlistJournal(dir, terminal string) error {
	pending := filepath.Join(dir, smartlistJournalName)
	data, err := os.ReadFile(pending)
	if err != nil {
		return err
	}
	if err := publishEntry(pending, filepath.Join(dir, terminal)); err != nil {
		// Rename may have happened before directory sync failed. Re-establish
		// the blocking intent before permitting any rollback attempt.
		return errors.Join(err, writeAtomic(pending, data))
	}
	return nil
}

func allowSmartlistRepair(ctx context.Context) error {
	report, err := systemcheck.Run(ctx, []string{systemcheck.EngineDesktop})
	if err != nil {
		return err
	}
	return systemcheck.AllowMutation(report)
}

func smartlistRepairStale() error {
	return &progress.Error{Code: "smartlist_repair_stale", Cause: errors.New("Smartlist repair changed since review; preview again")}
}
