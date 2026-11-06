package transfer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func readObservedCollection(ctx context.Context, q queryer, path string, projection *history.SmartlistProjection) (*collection, error) {
	c, err := readCollection(ctx, q, path, nil)
	if err != nil {
		return nil, err
	}
	if projection != nil {
		c.Smartlists = projection
		for key, e := range c.Entities {
			if e.Kind == "smartlist" {
				delete(c.Entities, key)
			}
		}
		for _, saved := range projection.Rows {
			key := "smartlist:" + str(saved.Values["listUuid"])
			e := &entity{Kind: "smartlist", Key: key, Values: without(row(saved.Values), "lastEditTime"), Parent: c.Playlists[saved.ParentPlaylistID]}
			if saved.ParentPlaylistID != 0 && e.Parent == "" {
				return nil, fmt.Errorf("Smartlist projection references a missing parent")
			}
			if next := str(saved.Values["nextListUuid"]); next != "" {
				e.Next = "smartlist:" + next
			} else if saved.NextPlaylistID != 0 {
				e.Next = c.Playlists[saved.NextPlaylistID]
				if e.Next == "" {
					return nil, fmt.Errorf("Smartlist projection references a missing successor")
				}
			}
			c.Entities[key] = e
		}
	}
	if err := readCompanions(ctx, c); err != nil {
		return nil, err
	}
	if err := readImports(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func observeSource(ctx context.Context, o *Options) (*collection, error) {
	database, err := openLibrary(o.Source, false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	return history.ReadSmartlistProjection(ctx, o.Source, database, func(ctx context.Context, tx *sql.Tx, projection *history.SmartlistProjection) (*collection, error) {
		if len(o.Scope.PlaylistRefs) > 0 {
			ids, err := libraryidentity.ResolveBatch(ctx, tx, o.Source, "playlist", o.Scope.PlaylistRefs)
			if err != nil {
				return nil, err
			}
			o.Scope.PlaylistIDs = ids
		}
		return readObservedCollection(ctx, tx, o.Source, projection)
	})
}

// The repaired destination must equal the reviewed projection before backup and
// again inside the final write reservation. Transfer-only identity remappings
// have not been published yet, so capture this digest before building the plan.
func observationDigest(c *collection) string {
	entities := make(map[string]*entity, len(c.Entities))
	for key, e := range c.Entities {
		copy := *e
		if copy.Kind == "smartlist" {
			// Native path values contain these links. Projected numeric identities
			// are planning metadata and are absent from the post-repair raw read.
			copy.Parent, copy.Next = "", ""
		}
		entities[key] = &copy
	}
	return digest([]any{c.UUID, c.Instance, c.Registry, entities, c.State, c.Schema, c.SmartlistSchema, c.Tags, c.Imports, c.Colors})
}

func checkDestinationObservation(ctx context.Context, q queryer, path, expected string) error {
	current, err := readObservedCollection(ctx, q, path, nil)
	if err != nil {
		return err
	}
	if observationDigest(current) != expected {
		return staleSync()
	}
	return nil
}

func staleSync() error {
	return &progress.Error{Code: "sync_preview_stale", Cause: fmt.Errorf("sync preview is stale; preview again before applying")}
}
