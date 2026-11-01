package main

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func seedTopology(ctx context.Context, d *sql.DB, g golden, name string) error {
	stable, err := db.CreatePlaylist(ctx, d, "Stable", 0, true)
	if err != nil {
		return err
	}
	if _, err := db.CreatePlaylist(ctx, d, "Anchor", stable, false); err != nil {
		return err
	}
	parent := "Missing;"
	if name != "unknown-removal" {
		house, err := db.CreatePlaylist(ctx, d, "House", 0, true)
		if err != nil {
			return err
		}
		if _, err := db.CreatePlaylist(ctx, d, "Leaf", house, false); err != nil {
			return err
		}
		parent = "House;"
	}
	// Insert tails first so each independent component exercises both native edge kinds.
	for _, row := range []struct{ id, name, parent, nextPath, nextUUID, rule string }{
		{healthyTailID, "T3 healthy tail", "Stable;", "Stable;Anchor;", "", "T3"},
		{healthyID, "T3 healthy Techno", "Stable;", "", healthyTailID, "T3"},
		{targetTailID, "T1 House tail", parent, parent + "Leaf;", "", "T1"},
		{targetID, "T1 House titles", parent, "", targetTailID, "T1"},
	} {
		if _, err := d.ExecContext(ctx, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES (?,?,?,?,?,?)`, row.id, row.name, row.parent, row.nextPath, row.nextUUID, rules(g, row.rule)); err != nil {
			return err
		}
	}
	return nil
}
