package api

import (
	"github.com/alexgorbatchev/engine-cli/internal/details"

	"github.com/alexgorbatchev/engine-cli/internal/track"
)

func addTrackResult(r *track.AddTrackResult) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{"track": details.Track(r.Track), "playlist": r.Playlist, "moved": r.Moved}
}

func upgradeResult(r *track.UpgradeResult) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{"track": details.Track(r.Track), "original_path": r.OriginalPath, "timing_cleared": r.TimingCleared, "reanalysis_required": true}
}
