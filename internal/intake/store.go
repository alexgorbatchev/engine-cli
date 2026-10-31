// Package intake owns durable, source-preserving local-file intake.
package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
)

const applicationID = 0x454E4749
const schemaVersion = 1

// Store is opened by a library owner and closed after its intake worker joins.
// Individual state changes use short transactions; worker ownership must never
// retain a write transaction on this database while preparing media.
type Store struct {
	database *sql.DB
	library  string
	lease    *intakelease.Lease
}

func Open(ctx context.Context, library string) (*Store, error) {
	lease, err := intakelease.Observe(ctx, library)
	if err != nil {
		return nil, err
	}
	d, created, err := companion.OpenWrite(ctx, library, companion.IntakeName)
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	d.SetMaxOpenConns(1)
	if err := initialize(ctx, d); err != nil {
		err = errors.Join(err, d.Close())
		if created {
			err = errors.Join(err, companion.Remove(filepath.Join(library, ".engine-cli", companion.IntakeName)))
		}
		return nil, errors.Join(err, lease.Close())
	}
	return &Store{database: d, library: library, lease: lease}, nil
}

func (s *Store) Close() error { return errors.Join(s.database.Close(), s.lease.Close()) }

func initialize(ctx context.Context, d *sql.DB) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var application, version, tables int
	err = tx.QueryRowContext(ctx, `SELECT application_id,user_version,
 (SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%')
 FROM pragma_application_id,pragma_user_version`).Scan(&application, &version, &tables)
	if err != nil {
		return err
	}
	if application == applicationID && version == schemaVersion {
		// Query the full projection before accepting an existing queue. A valid
		// header cannot turn a truncated or foreign schema into an empty queue.
		rows, err := tx.QueryContext(ctx, "SELECT "+itemColumns+" FROM Item LIMIT 0")
		if err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, "SELECT item_id,history_id,plan_token,to_playlist,track_id FROM ImportIntent LIMIT 0")
		if err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := intakecontrol.Read(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	if application != 0 || version != 0 || tables != 0 {
		return fmt.Errorf("not a supported Engine intake database")
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := intakecontrol.Create(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", applicationID, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

const schema = `CREATE TABLE Item (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 source_path TEXT NOT NULL,
 source_identity TEXT NOT NULL,
 source_size INTEGER NOT NULL CHECK(source_size>=0),
 source_modified INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('queued','preparing','staged','failed','cancelled','importing','imported')),
 attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt>=0),
 cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
 stage_id TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 history_id TEXT NOT NULL DEFAULT '',
 UNIQUE(source_path,source_identity,source_size,source_modified)
) STRICT;
CREATE TABLE ImportIntent (
 item_id INTEGER PRIMARY KEY REFERENCES Item(id),
 history_id TEXT NOT NULL UNIQUE,
 plan_token TEXT NOT NULL,
 to_playlist TEXT NOT NULL,
 track_id INTEGER CHECK(track_id>0)
) STRICT;`
