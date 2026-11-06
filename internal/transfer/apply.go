package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// applyPlan publishes the accepted plan within the History operation's native
// transaction. Source native/history capture finished before destination ownership.
func applyPlan(ctx context.Context, o Options, p *plan, conn *sql.Tx) error {
	s, d := p.source, p.destination
	var err error
	if err := p.apply(ctx, conn); err != nil {
		return err
	}
	if err := p.validateSmartlists(ctx, conn); err != nil {
		return &progress.Error{Code: "sync_destination_validation_failed", Cause: err}
	}
	// Persist baselines calculated from actual rows after Engine triggers execute.
	after, err := readCollection(ctx, conn, o.Destination, &p.destination.State)
	if err != nil {
		return err
	}
	for key, saved := range p.expectedSmartlists {
		// validateSmartlists already matched actual complete rows and native
		// paths. Restore their resolved identities for persistent baselines.
		after.Entities[key].Parent, after.Entities[key].Next = saved.Parent, saved.Next
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
	for _, captured := range []*collection{s, d} {
		if err := checkCompanions(ctx, captured); err != nil {
			return err
		}
	}
	for _, c := range []*collection{s, d} {
		if err := validateImportRevision(ctx, c); err != nil {
			return err
		}
	}
	if err = conn.Commit(); err != nil {
		return err
	}
	if err := p.publishCompanions(ctx); err != nil {
		return err
	}
	if next := p.plannedImports(); next.Revision() != d.Imports.Revision() {
		if err := importsource.Replace(ctx, o.Destination, d.Imports.Revision(), next); err != nil {
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
func writeRow(ctx context.Context, c *sql.Tx, table string, v row, idColumn string, id any) (int64, error) {
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
