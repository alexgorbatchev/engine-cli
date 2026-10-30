package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

// Run returns the same comparison for CLI and HTTP. Only apply creates history.
func Run(ctx context.Context, o Options) (*Report, error) {
	o, err := normalize(o)
	if err != nil {
		return nil, err
	}
	source, err := openLibrary(o.Source, false)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	if len(o.Scope.PlaylistRefs) > 0 {
		ids, err := libraryidentity.ResolveBatch(ctx, source, o.Source, "playlist", o.Scope.PlaylistRefs)
		if err != nil {
			return nil, err
		}
		o.Scope.PlaylistIDs = ids
	}
	dest, err := openLibrary(o.Destination, !o.DryRun)
	if err != nil {
		return nil, err
	}
	defer dest.Close()
	src, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer src.Rollback()
	conn, err := dest.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	begin := "BEGIN"
	if !o.DryRun {
		begin = "BEGIN IMMEDIATE"
	}
	if _, err = conn.ExecContext(ctx, begin); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") // Already committed transactions require no rollback.
	progress.Stage(ctx, "sync.compare", 5)
	s, err := readCollection(ctx, src, o.Source, nil)
	if err != nil {
		return nil, err
	}
	d, err := readCollection(ctx, conn, o.Destination, nil)
	if err != nil {
		return nil, err
	}
	// Companion state is captured before the Engine transaction changes track
	// identities. The post-apply Engine read below must not reinterpret the old
	// companion against rows that have already been removed by this plan.
	if err := readTags(ctx, s); err != nil {
		return nil, err
	}
	if err := readTags(ctx, d); err != nil {
		return nil, err
	}
	p, err := buildPlan(ctx, o, s, d)
	if err != nil {
		return nil, err
	}
	if o.PlanToken != "" && o.PlanToken != p.report.PlanToken {
		return p.report, fmt.Errorf("sync preview is stale; preview again before applying")
	}
	if o.DryRun {
		return p.report, nil
	}
	if !p.report.Applicable {
		return p.report, fmt.Errorf("sync has %d unresolved conflicts; preview and resolve them before applying", len(p.report.Conflicts))
	}
	if len(p.report.Changes) == 0 && len(p.copy) == 0 && len(p.artwork) == 0 && reflect.DeepEqual(d.State.peer(s), p.baseline) {
		return p.report, nil
	}
	progress.Stage(ctx, "sync.backup", 20)
	if _, err = backup.AutoBackupIfNeeded(ctx, o.Destination, o.NoBackup); err != nil {
		return p.report, err
	}
	entry, err := history.Run(ctx, o.Destination, "library.sync", history.ScopeCollection, nil, func(ctx context.Context) error {
		defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") // Release failed writes before history captures the result.
		if err := p.apply(ctx, conn); err != nil {
			return err
		}
		// Persist baselines calculated from actual rows after Engine triggers execute.
		after, err := readCollection(ctx, conn, o.Destination, &p.destination.State)
		if err != nil {
			return err
		}
		after.State = d.State
		for id, key := range p.destination.Playlists {
			after.State.Playlists[strconv.FormatInt(id, 10)] = key
		}
		after.State.Peers[s.Instance] = p.baseline
		after.State.Revisions[s.Instance] = max(s.State.revision(d), d.State.revision(s)) + 1
		for key, b := range p.baseline {
			if e := after.Entities[key]; e != nil {
				if isTag(e) {
					continue
				}
				if e.Kind == "track" {
					e.Hash, e.Size, err = fileHash(ctx, e.File)
					if err != nil {
						return err
					}
				}
				b.Destination = fingerprint(e)
				after.State.Peers[s.Instance][key] = b
			}
		}
		if err = history.Preserve(ctx, statePath(o.Destination)); err != nil {
			return err
		}
		// The Engine transaction and companion cannot commit atomically in WAL
		// mode. Validate both snapshots before the first commit; publication
		// failures thereafter deliberately retain pending history for recovery.
		for _, check := range []struct {
			path     string
			expected string
		}{{o.Source, s.Tags.Revision()}, {o.Destination, d.Tags.Revision()}} {
			current, err := tags.Read(ctx, check.path)
			if err != nil {
				return err
			}
			if current.Revision() != check.expected {
				return &progress.Error{Code: "tag_revision_stale", Cause: fmt.Errorf("custom tags changed while applying the sync plan")}
			}
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		nextTags := p.plannedTags()
		if nextTags.Revision() != d.Tags.Revision() {
			if _, err := tags.Replace(ctx, o.Destination, d.Tags.Revision(), nextTags); err != nil {
				return errors.Join(err, history.RequireRecovery(ctx))
			}
		}
		if d.Registry != nil {
			for id, key := range after.State.Playlists {
				d.Registry.Playlists[id] = strings.TrimPrefix(key, "playlist:")
			}
			if err := history.WriteJSON(ctx, libraryidentity.Path(o.Destination), d.Registry); err != nil {
				return errors.Join(err, history.RequireRecovery(ctx))
			}
		}
		if err := writeState(o.Destination, after.State); err != nil {
			return errors.Join(err, history.RequireRecovery(ctx))
		}
		return nil
	})
	if entry != nil {
		p.report.OperationID = entry.ID
	}
	return p.report, err
}

func writeState(path string, s state) error {
	p := statePath(path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), "state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}
func writeRow(ctx context.Context, c *sql.Conn, table string, v row, idColumn string, id any) (int64, error) {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	names := []string{}
	marks := []string{}
	args := []any{}
	sets := []string{}
	for _, k := range keys {
		names = append(names, quoted(k))
		marks = append(marks, "?")
		sets = append(sets, quoted(k)+"=?")
		args = append(args, v[k])
	}
	var result sql.Result
	var err error
	if id == nil {
		result, err = c.ExecContext(ctx, "INSERT INTO "+quoted(table)+" ("+strings.Join(names, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...)
	} else {
		args = append(args, id)
		result, err = c.ExecContext(ctx, "UPDATE "+quoted(table)+" SET "+strings.Join(sets, ",")+" WHERE "+quoted(idColumn)+"=?", args...)
	}
	if err != nil {
		return 0, fmt.Errorf("writing %s: %w", table, err)
	}
	if id != nil {
		return 0, nil
	}
	return result.LastInsertId()
}
