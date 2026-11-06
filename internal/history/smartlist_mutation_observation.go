package history

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

// observeSmartlistMutation is called only after acquiring history publication
// ownership. The native write reservation keeps projection, repair evidence and
// the caller's comparison on the same generation without changing any rows.
func observeSmartlistMutation(ctx context.Context, lib string, observe func(context.Context, *sql.Tx, *SmartlistProjection) (bool, error)) (token string, proceed bool, err error) {
	path := filepath.ToSlash(filepath.Join(lib, "Database2", "m.db"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)"}, "_txlock": {"immediate"}}.Encode()}
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	// A failed native rollback must never leave a pooled transaction behind.
	database.SetMaxIdleConns(0)
	conn, err := database.Conn(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	projection, err := projectSmartlists(ctx, lib, tx)
	if err != nil {
		return "", false, err
	}
	repair, err := planSmartlistRepair(ctx, lib)
	if err != nil {
		return "", false, err
	}
	proceed, err = observe(ctx, tx, projection)
	if err != nil {
		return "", false, err
	}
	return repair.review.PlanToken, proceed, nil
}
