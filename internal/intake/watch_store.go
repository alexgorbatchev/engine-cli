package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/watchclock"
	"github.com/alexgorbatchev/engine-cli/internal/watchcontrol"
)

const watchApplicationID = watchcontrol.ApplicationID
const watchSchemaVersion = watchcontrol.SchemaVersion

const watchSchema = `CREATE TABLE WatchControl (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>=0),
 reason TEXT NOT NULL CHECK(reason IN ('','restored'))
) STRICT;
INSERT INTO WatchControl VALUES(1,0,'');
CREATE TABLE WatchRoot (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 path TEXT NOT NULL,
 identity TEXT NOT NULL,
 recursive INTEGER NOT NULL CHECK(recursive IN (0,1)),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN (0,1)),
 phase TEXT NOT NULL DEFAULT 'idle' CHECK(phase IN ('idle','scanning','degraded')),
 last_scan_at INTEGER,
 error_code TEXT NOT NULL DEFAULT '',
 observed INTEGER NOT NULL DEFAULT 0 CHECK(observed>=0)
) STRICT;
CREATE UNIQUE INDEX WatchRootIdentity ON WatchRoot(identity) WHERE retired=0;
CREATE TABLE WatchVersion (
 id INTEGER PRIMARY KEY,
 identity TEXT NOT NULL,
 size INTEGER NOT NULL CHECK(size>=0),
 modified INTEGER NOT NULL,
 stable_since INTEGER NOT NULL,
 UNIQUE(identity,size,modified)
) STRICT;
CREATE TABLE WatchAlias (
 version_id INTEGER NOT NULL REFERENCES WatchVersion(id),
 root_id TEXT NOT NULL REFERENCES WatchRoot(id),
 path TEXT NOT NULL,
 seen_at INTEGER NOT NULL,
 PRIMARY KEY(root_id,path)
) STRICT;
CREATE TABLE WatchHandoff (
 version_id INTEGER PRIMARY KEY REFERENCES WatchVersion(id),
 source_path TEXT NOT NULL,
 source_identity TEXT NOT NULL,
 source_size INTEGER NOT NULL CHECK(source_size>=0),
 source_modified INTEGER NOT NULL,
 item_id INTEGER,
 provenance TEXT NOT NULL CHECK(provenance IN ('pending','created','associated','unknown')),
 CHECK((item_id IS NULL AND provenance='pending') OR (item_id>0 AND provenance<>'pending'))
) STRICT;`

func openWatch(ctx context.Context, library string) (*sql.DB, error) {
	d, created, err := companion.OpenWrite(ctx, library, companion.WatchName)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if err := initializeWatch(ctx, d); err != nil {
		err = errors.Join(err, d.Close())
		if created {
			err = errors.Join(err, companion.Remove(filepath.Join(library, ".engine-cli", companion.WatchName)))
		}
		return nil, err
	}
	return d, nil
}

func initializeWatch(ctx context.Context, d *sql.DB) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var application, version, tables int
	if err := tx.QueryRowContext(ctx, `SELECT application_id,user_version,
 (SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%')
 FROM pragma_application_id,pragma_user_version`).Scan(&application, &version, &tables); err != nil {
		return err
	}
	if application == 0 && version == 0 && tables == 0 {
		if _, err := tx.ExecContext(ctx, watchSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", watchApplicationID, watchSchemaVersion)); err != nil {
			return err
		}
	} else if application != watchApplicationID || version != watchSchemaVersion {
		return fmt.Errorf("not a supported Engine watched-folder database")
	}
	if _, err := readWatchState(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

type watchReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// WatchList never enrolls a companion or starts a service as a side effect.
func (s *Store) WatchList(ctx context.Context) (state WatchState, err error) {
	state = WatchState{Manager: "inactive", Roots: []WatchRoot{}}
	present, err := intakestate.Observe(ctx, s.library, func(ctx context.Context) error {
		state, err = s.watchListHeld(ctx)
		return err
	})
	if err != nil || present {
		return state, err
	}
	path, err := companion.Database(s.library, companion.WatchName)
	if err != nil {
		return state, err
	}
	authority, err := watchclock.Read(ctx, s.library)
	if err == nil && (path != "" || authority.Revision != 0) {
		err = watchFailure("watch_recovery_required", errors.New("watch authority is missing its state owner"))
	}
	return state, err
}

// watchListHeld is used only inside an existing native state owner. Acquiring
// another shared observation there would deadlock against our own writer.
func (s *Store) watchListHeld(ctx context.Context) (state WatchState, err error) {
	state, err = s.readWatchImage(ctx)
	if err != nil {
		return state, err
	}
	authority, err := watchclock.Read(ctx, s.library)
	if state.Revision < authority.Restored {
		state.Suspended, state.Reason = true, "restored"
		for i := range state.Roots {
			if state.Roots[i].Enabled {
				state.Roots[i].Phase = "suspended"
			}
		}
	}
	state.Revision = max(state.Revision, authority.Revision)
	return state, err
}

func (s *Store) readWatchImage(ctx context.Context) (state WatchState, err error) {
	state = WatchState{Manager: "inactive", Roots: []WatchRoot{}}
	path, err := companion.Database(s.library, companion.WatchName)
	if err != nil || path == "" {
		return state, err
	}
	d, err := db.Open(path, true)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	if err := watchHeader(ctx, tx); err != nil {
		return state, err
	}
	state, err = readWatchState(ctx, tx)
	if err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func watchHeader(ctx context.Context, d watchReader) error {
	var application, version int
	if err := d.QueryRowContext(ctx, "SELECT application_id,user_version FROM pragma_application_id,pragma_user_version").Scan(&application, &version); err != nil {
		return err
	}
	if application != watchApplicationID || version != watchSchemaVersion {
		return errors.New("not a supported Engine watched-folder database")
	}
	return nil
}

func readWatchState(ctx context.Context, d watchReader) (WatchState, error) {
	state := WatchState{Manager: "inactive", Roots: []WatchRoot{}}
	if err := d.QueryRowContext(ctx, "SELECT revision,reason FROM WatchControl WHERE id=1").Scan(&state.Revision, &state.Reason); err != nil {
		return state, err
	}
	if state.Revision < 0 || state.Reason != "" && state.Reason != "restored" {
		return state, fmt.Errorf("invalid watched-folder control")
	}
	state.Suspended = state.Reason != ""
	rows, err := d.QueryContext(ctx, `SELECT r.id,r.path,r.recursive,r.enabled,r.phase,r.last_scan_at,r.error_code,r.observed,
 (SELECT count(DISTINCT a.version_id) FROM WatchAlias a LEFT JOIN WatchHandoff h ON h.version_id=a.version_id WHERE a.root_id=r.id AND h.item_id IS NULL),
 (SELECT count(DISTINCT a.version_id) FROM WatchAlias a JOIN WatchHandoff h ON h.version_id=a.version_id WHERE a.root_id=r.id AND h.item_id IS NOT NULL)
 FROM WatchRoot r WHERE retired=0 ORDER BY sequence`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var root WatchRoot
		var scanned sql.NullInt64
		if err := rows.Scan(&root.ID, &root.Path, &root.Recursive, &root.Enabled, &root.Phase, &scanned, &root.ErrorCode, &root.Observed, &root.Pending, &root.Linked); err != nil {
			return state, err
		}
		if !root.Enabled {
			root.Phase = "disabled"
		} else if state.Suspended {
			root.Phase = "suspended"
		}
		if scanned.Valid {
			value := time.Unix(0, scanned.Int64).UTC()
			root.LastScanAt = &value
		}
		state.Roots = append(state.Roots, root)
	}
	return state, rows.Err()
}

func (s *Store) changeWatch(ctx context.Context, revision int64, run func(context.Context, *sql.Tx) error) (state WatchState, err error) {
	err = intakestate.With(ctx, s.library, func(ctx context.Context) (err error) {
		state, err = s.watchListHeld(ctx)
		if err != nil {
			return err
		}
		if revision != state.Revision {
			return watchFailure("watch_revision_changed", errors.New("watch configuration revision changed"))
		}
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if state.Suspended {
			if _, err := tx.ExecContext(ctx, "UPDATE WatchControl SET reason='restored' WHERE id=1"); err != nil {
				return err
			}
		}
		if err := run(ctx, tx); err != nil {
			return err
		}
		allocated, err := watchclock.Advance(ctx, s.library, state.Revision)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE WatchControl SET revision=? WHERE id=1", allocated); err != nil {
			return err
		}
		state, err = readWatchState(ctx, tx)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return nil
	})
	return state, err
}

func watchFailure(code string, err error) error { return &progress.Error{Code: code, Cause: err} }
