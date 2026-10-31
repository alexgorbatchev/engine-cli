package duplicate

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

func attachReferences(ctx context.Context, d *sql.DB, tracks []Track) error {
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return err
	}
	state, err := colors.Read(ctx, lib)
	if err != nil {
		return err
	}
	assigned := map[companion.Origin]string{}
	for _, a := range state.Assignments {
		assigned[companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}] = a.Color
	}
	for i := range tracks {
		tracks[i].Color = assigned[companion.Origin{UUID: tracks[i].originUUID, ID: tracks[i].originID}]
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
