package api

import "github.com/alexgorbatchev/engine-cli/internal/track"

// Mutation results report what the operation changed, using values the
// operation already holds. Consumers read changed entities separately, so a
// committed change is never reported as failed because a follow-up read failed.

type changedTrack struct {
	TrackID int64 `json:"track_id"`
}

type movedTrack struct {
	TrackID int64  `json:"track_id"`
	Path    string `json:"path"`
}

type addedTrack struct {
	TrackID    int64  `json:"track_id"`
	Path       string `json:"path"`
	PlaylistID int64  `json:"playlist_id,omitempty"`
	Moved      bool   `json:"moved"`
}

func newAddedTrack(r *track.AddTrackResult) addedTrack {
	added := addedTrack{TrackID: r.Track.ID, Path: r.Track.PathString(), Moved: r.Moved}
	if r.Playlist != nil {
		added.PlaylistID = r.Playlist.ID
	}
	return added
}

type membershipChange struct {
	PlaylistID int64 `json:"playlist_id"`
	TrackID    int64 `json:"track_id"`
}

type membershipMove struct {
	FromPlaylistID int64 `json:"from_playlist_id"`
	ToPlaylistID   int64 `json:"to_playlist_id"`
	TrackID        int64 `json:"track_id"`
}

// UpgradedTrack reports where replacement audio was installed and what it cleared.
type UpgradedTrack struct {
	TrackID            int64  `json:"track_id"`
	Path               string `json:"path"`
	OriginalPath       string `json:"original_path"`
	TimingCleared      bool   `json:"timing_cleared"`
	ReanalysisRequired bool   `json:"reanalysis_required"`
}

func newUpgradedTrack(r *track.UpgradeResult) UpgradedTrack {
	return UpgradedTrack{TrackID: r.Track.ID, Path: r.Track.PathString(), OriginalPath: r.OriginalPath, TimingCleared: r.TimingCleared, ReanalysisRequired: true}
}
