// Package watchcontrol validates portable discovery control and fences native
// replacement without importing the discovery worker or its History owner.
package watchcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/watchclock"
)

const ApplicationID = 0x454E4757
const SchemaVersion = 1

// Reader is implemented by a watch database and its validation transaction.
type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type State struct {
	Revision int64
	Reason   string
}

func Read(ctx context.Context, d Reader) (state State, err error) {
	var application, version int
	if err := d.QueryRowContext(ctx, "SELECT application_id,user_version FROM pragma_application_id,pragma_user_version").Scan(&application, &version); err != nil {
		return state, err
	}
	if application != ApplicationID || version != SchemaVersion {
		return state, errors.New("not a supported Engine watched-folder database")
	}
	if err := d.QueryRowContext(ctx, "SELECT revision,reason FROM WatchControl WHERE id=1").Scan(&state.Revision, &state.Reason); err != nil {
		return state, err
	}
	if state.Revision < 0 || state.Reason != "" && state.Reason != "restored" {
		return state, errors.New("invalid watched-folder control")
	}
	return state, nil
}

func imageState(ctx context.Context, path string) (state State, err error) {
	if path == "" {
		return state, nil
	}
	d, err := db.Open(path, true)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	return Read(ctx, d)
}

// Fence commits before the first live replacement, after all selected images
// have been validated. The caller holds exclusive intake state ownership until
// all pair writes and portable markers have completed or recovery is retained.
func Fence(ctx context.Context, library, selected string) (int64, error) {
	image, err := imageState(ctx, selected)
	if err != nil {
		return 0, err
	}
	path, err := companion.Database(library, companion.WatchName)
	if err != nil {
		return 0, err
	}
	live, err := imageState(ctx, path)
	if err != nil {
		return 0, err
	}
	return watchclock.Restore(ctx, library, max(image.Revision, live.Revision))
}

// Restored marks a present portable image using the already committed fence.
// Absence remains absence; the retained authority watermark suspends it.
func Restored(ctx context.Context, library string, revision int64) (err error) {
	path, err := companion.Database(library, companion.WatchName)
	if err != nil || path == "" {
		return err
	}
	d, err := db.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := Read(ctx, tx)
	if err != nil {
		return err
	}
	if revision <= state.Revision {
		return fmt.Errorf("restored image exceeds its authorization fence")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE WatchControl SET revision=?,reason='restored' WHERE id=1", revision); err != nil {
		return err
	}
	return tx.Commit()
}
