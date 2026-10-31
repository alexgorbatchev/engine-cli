package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// CheckSmartlistRepair checks intent without enrolling storage or recovering it.
// It is a preflight only: coherent readers and writers must also hold the history
// lock across this check and their entire observation or publication.
func CheckSmartlistRepair(ctx context.Context, lib string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := storeDir(lib)
	if err != nil {
		return err
	}
	return checkSmartlistRepair(dir)
}

func checkSmartlistRepair(historyDir string) error {
	dir := filepath.Join(filepath.Dir(historyDir), "smartlist-repair")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return smartlistRepairPending(errors.New("Smartlist repair storage is not a directory"))
	}
	_, err = os.Lstat(filepath.Join(dir, smartlistJournalName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Neither force nor malformed intent grants permission to observe a repair
	// halfway through publication. Only the protected recovery owner resolves it.
	return smartlistRepairPending(errors.New("Smartlist repair publication is incomplete"))
}

func smartlistRepairPending(cause error) error {
	return &progress.Error{Code: "smartlist_repair_pending", Cause: cause}
}

func listSmartlistSafeHistory(dir string) ([]Entry, error) {
	lib := filepath.Dir(filepath.Dir(dir))
	return ReadSmartlistSafe(context.Background(), lib, func(context.Context) ([]Entry, error) {
		return entries(dir)
	})
}
