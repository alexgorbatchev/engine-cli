package intake

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type watchHandoff struct {
	VersionID  int64
	Source     staging.LocalSource
	ItemID     sql.NullInt64
	Provenance string
}

// publishWatchHandoff keeps a recoverable journal in watch.db before inserting
// into intake.db. Both commits are covered by the same short state owner, so a
// companion snapshot cannot capture half a successful handoff. Process death
// may leave the pending journal; restart resolves the exact native version.
func (s *Store) publishWatchHandoff(ctx context.Context, versionID int64, source staging.LocalSource, quiet time.Duration, validate func() error) (item *Item, provenance string, published bool, err error) {
	err = intakestate.With(ctx, s.library, func(ctx context.Context) (err error) {
		control, err := s.Control(ctx)
		if err != nil || control.Paused {
			return err
		}
		state, err := s.watchListHeld(ctx)
		if err != nil || state.Suspended {
			return err
		}
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		var authorized bool
		if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM WatchAlias a
 JOIN WatchRoot r ON r.id=a.root_id JOIN WatchVersion v ON v.id=a.version_id
 WHERE a.version_id=? AND a.path=? AND r.enabled=1 AND r.retired=0
 AND v.identity=? AND v.size=? AND v.modified=? AND v.stable_since<=?)`, versionID, source.Path, source.Identity, source.Size, source.Modified, time.Now().Add(-quiet).UnixNano()).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return nil
		}
		if err := validate(); err != nil {
			return err
		}
		handoff, err := readWatchHandoff(ctx, d, versionID)
		recovering := err == nil && !handoff.ItemID.Valid
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = d.ExecContext(ctx, `INSERT INTO WatchHandoff
 (version_id,source_path,source_identity,source_size,source_modified,provenance) VALUES(?,?,?,?,?,'pending')`,
				versionID, source.Path, source.Identity, source.Size, source.Modified); err != nil {
				return err
			}
			handoff = watchHandoff{VersionID: versionID, Source: source, Provenance: "pending"}
		} else if err != nil {
			return err
		}
		if handoff.Source.Identity != source.Identity || handoff.Source.Size != source.Size || handoff.Source.Modified != source.Modified {
			return watchFailure("watch_recovery_required", errors.New("watch handoff differs from its captured native version"))
		}
		if handoff.ItemID.Valid {
			item, err = scanItem(s.database.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM Item WHERE id=?", handoff.ItemID.Int64))
			if err != nil {
				return watchFailure("watch_recovery_required", err)
			}
			if !watchItemMatches(item, handoff.Source) {
				return watchFailure("watch_recovery_required", errors.New("linked intake item differs from watch receipt"))
			}
			provenance = handoff.Provenance
			return nil
		}
		// An earlier pending publication or a manual enqueue may already own this
		// native version under another spelling. Never reset its tuple or state.
		item, err = scanItem(s.database.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM Item WHERE source_identity=? AND source_size=? AND source_modified=? ORDER BY id LIMIT 1",
			source.Identity, source.Size, source.Modified))
		provenance = "associated"
		if recovering {
			provenance = "unknown"
		}
		if errors.Is(err, sql.ErrNoRows) {
			var created bool
			item, created, err = s.enqueueVersion(ctx, handoff.Source)
			if created {
				provenance = "created"
			}
		}
		if err != nil {
			return err
		}
		_, err = d.ExecContext(ctx, `UPDATE WatchHandoff SET source_path=?,source_identity=?,source_size=?,source_modified=?,item_id=?,provenance=?
 WHERE version_id=? AND item_id IS NULL`, item.SourcePath, item.SourceIdentity, item.SourceSize, item.SourceModified, item.ID, provenance, versionID)
		published = err == nil
		return err
	})
	return item, provenance, published, err
}

func readWatchHandoff(ctx context.Context, d *sql.DB, versionID int64) (watchHandoff, error) {
	h := watchHandoff{VersionID: versionID}
	err := d.QueryRowContext(ctx, `SELECT source_path,source_identity,source_size,source_modified,item_id,provenance
 FROM WatchHandoff WHERE version_id=?`, versionID).Scan(&h.Source.Path, &h.Source.Identity, &h.Source.Size, &h.Source.Modified, &h.ItemID, &h.Provenance)
	return h, err
}

func watchItemMatches(item *Item, source staging.LocalSource) bool {
	return item.SourcePath == source.Path && item.SourceIdentity == source.Identity && item.SourceSize == source.Size && item.SourceModified == source.Modified
}
