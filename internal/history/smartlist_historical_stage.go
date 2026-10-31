package history

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// A historical amendment is scoped to one original deletion image, never a
// permission to relax current-live validation. The repair owner must derive
// patches from proven deletion-time identities before constructing this plan.
type smartlistHistoricalAmendment struct {
	entryID string
	image   databaseState
	patches []smartlistPatch
}

func stageHistoricalSmartlistImage(ctx context.Context, destination string, amendment smartlistHistoricalAmendment) (string, error) {
	if amendment.entryID == "" || amendment.image.Path == "" || amendment.image.Hash == "" {
		return "", errors.New("historical Smartlist amendment lacks original entry/image evidence")
	}
	return stageSmartlistRows(ctx, amendment.image.Path, destination, amendment.image.Hash, amendment.patches, func(before, after *db.SmartlistInventory) error {
		return validateHistoricalSmartlistStage(before, after, amendment.patches)
	})
}

// Complete native rows/schema are checked by the shared stage owner. This
// additional proof permits unrelated old damage, while every surviving selected
// row is healthy and no unchanged row gains a new failed relationship witness.
func validateHistoricalSmartlistStage(before, after *db.SmartlistInventory, patches []smartlistPatch) error {
	if before.EngineUUID != after.EngineUUID || before.PlaylistSchema != after.PlaylistSchema || !reflect.DeepEqual(before.Playlists, after.Playlists) {
		return errors.New("historical Smartlist amendment changed native playlist identity")
	}
	selected := make(map[string]bool, len(patches))
	for _, patch := range patches {
		selected[patch.before.UUID] = true
	}
	existing := make(map[string]bool, len(before.Causes))
	type anchor struct{ parent, next, uuid string }
	collisions := make(map[anchor]map[string]bool)
	for _, cause := range before.Causes {
		if cause.Reason == "predecessor_collision" {
			members := make(map[string]bool, len(cause.Members))
			for _, id := range cause.Members {
				members[id] = true
			}
			collisions[anchor{cause.ParentPath, cause.NextPath, cause.NextUUID}] = members
		} else {
			key, err := json.Marshal(cause)
			if err != nil {
				return err
			}
			existing[string(key)] = true
		}
	}
	for _, cause := range after.Causes {
		if cause.Reason == "predecessor_collision" {
			original := collisions[anchor{cause.ParentPath, cause.NextPath, cause.NextUUID}]
			remaining := make(map[string]bool, len(cause.Members))
			for _, id := range cause.Members {
				if selected[id] || !original[id] {
					return errors.New("historical Smartlist amendment creates a predecessor collision")
				}
				remaining[id] = true
			}
			for id := range original {
				if !remaining[id] && !selected[id] {
					return errors.New("historical Smartlist amendment changed an unrelated predecessor")
				}
			}
			continue
		}
		if selected[cause.UUID] {
			return errors.New("historical Smartlist amendment leaves selected row damaged")
		}
		key, err := json.Marshal(cause)
		if err != nil {
			return err
		}
		if !existing[string(key)] {
			return errors.New("historical Smartlist amendment creates or worsens unrelated damage")
		}
	}
	return nil
}
