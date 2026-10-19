package duplicate

import (
	"context"
	"database/sql"

	"github.com/dj/engine-cli/internal/identity"
)

func attachReferences(ctx context.Context, d *sql.DB, tracks []Track) error {
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return err
	}
	r, err := identity.Load(lib)
	if err != nil || r == nil {
		return err
	}
	entities, err := identity.List(ctx, d, lib)
	if err != nil {
		return err
	}
	refs := map[int64]string{}
	for _, e := range entities {
		if e.Kind == "track" {
			refs[e.ID] = e.Ref
		}
	}
	for i := range tracks {
		tracks[i].Ref = refs[tracks[i].ID]
	}
	return nil
}
