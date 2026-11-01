package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func seedHistoricalDamage(ctx context.Context, d *sql.DB, lib string, f *fixture) error {
	before, err := snapshot(ctx, d)
	if err != nil {
		return err
	}
	f.BeforeOperation = before
	op, statement := "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE title='House'`
	if f.Name == "original-deletion" {
		op, statement = "playlist.rm", `DELETE FROM Playlist WHERE title IN ('House','Leaf')`
	}
	// Model historical raw damage: the production hierarchy owner now updates saved rows.
	e, err := history.Run(ctx, lib, op, history.ScopeCollection, nil, func(ctx context.Context) error {
		if err := absentDesktop(ctx); err != nil {
			return err
		}
		_, err := d.ExecContext(ctx, statement)
		return err
	})
	if err != nil {
		return err
	}
	f.EntryID = e.ID
	entry, err := os.ReadFile(filepath.Join(lib, ".engine-cli", "history", e.ID+".json"))
	if err != nil {
		return err
	}
	f.EntrySHA256 = digest(entry)
	return nil
}
