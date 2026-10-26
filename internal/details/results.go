package details

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

func valueOrNull[T any](value T, valid bool) any {
	if !valid {
		return nil
	}
	return value
}

// Track converts t and the IDs of playlists that directly contain it. HTTP
// consumers receive JSON values and nulls, not database/sql wrappers.
func Track(t *db.Track, playlistIDs []int64) map[string]any {
	if t == nil {
		return nil
	}
	if playlistIDs == nil {
		playlistIDs = []int64{}
	}
	return map[string]any{
		"id":                 t.ID,
		"playOrder":          valueOrNull(t.PlayOrder.Int64, t.PlayOrder.Valid),
		"length":             valueOrNull(t.Length.Int64, t.Length.Valid),
		"bpm":                valueOrNull(t.BPM.Int64, t.BPM.Valid),
		"year":               valueOrNull(t.Year.Int64, t.Year.Valid),
		"path":               valueOrNull(t.Path.String, t.Path.Valid),
		"filename":           valueOrNull(t.Filename.String, t.Filename.Valid),
		"bitrate":            valueOrNull(t.Bitrate.Int64, t.Bitrate.Valid),
		"bpmAnalyzed":        valueOrNull(t.BPMAnalyzed.Float64, t.BPMAnalyzed.Valid),
		"albumArtId":         valueOrNull(t.AlbumArtID.Int64, t.AlbumArtID.Valid),
		"hasArtwork":         valueOrNull(t.HasArtwork.Bool, t.HasArtwork.Valid),
		"fileBytes":          valueOrNull(t.FileBytes.Int64, t.FileBytes.Valid),
		"title":              valueOrNull(t.Title.String, t.Title.Valid),
		"artist":             valueOrNull(t.Artist.String, t.Artist.Valid),
		"album":              valueOrNull(t.Album.String, t.Album.Valid),
		"genre":              valueOrNull(t.Genre.String, t.Genre.Valid),
		"comment":            valueOrNull(t.Comment.String, t.Comment.Valid),
		"label":              valueOrNull(t.Label.String, t.Label.Valid),
		"composer":           valueOrNull(t.Composer.String, t.Composer.Valid),
		"remixer":            valueOrNull(t.Remixer.String, t.Remixer.Valid),
		"key":                valueOrNull(t.Key.Int64, t.Key.Valid),
		"rating":             valueOrNull(t.Rating.Int64, t.Rating.Valid),
		"ratingStars":        min(track.MaxRatingStars, max(0, t.Rating.Int64/track.RatingUnitsPerStar)),
		"albumArt":           valueOrNull(t.AlbumArt.String, t.AlbumArt.Valid),
		"fileType":           valueOrNull(t.FileType.String, t.FileType.Valid),
		"isAnalyzed":         valueOrNull(t.IsAnalyzed.Bool, t.IsAnalyzed.Valid),
		"isAvailable":        valueOrNull(t.IsAvailable.Bool, t.IsAvailable.Valid),
		"lastEditTime":       valueOrNull(t.LastEditTime.String, t.LastEditTime.Valid),
		"albumArtSourceHash": valueOrNull(t.AlbumArtSourceHash.String, t.AlbumArtSourceHash.Valid),
		"playlistIds":        playlistIDs,
	}
}

// Tracks converts tracks after one membership lookup shared by all of them.
// Nil tracks stay nil, matching Track.
func Tracks(ctx context.Context, q db.Querier, tracks ...*db.Track) ([]map[string]any, error) {
	memberships, err := PlaylistIDs(ctx, q, tracks...)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, len(tracks))
	for i, t := range tracks {
		if t != nil {
			result[i] = Track(t, memberships[t.ID])
		}
	}
	return result, nil
}

// OneTrack converts a single track with its playlist memberships.
func OneTrack(ctx context.Context, q db.Querier, t *db.Track) (map[string]any, error) {
	if t == nil {
		return nil, nil
	}
	result, err := Tracks(ctx, q, t)
	if err != nil {
		return nil, err
	}
	return result[0], nil
}

// PlaylistIDs maps each track's local ID to the ascending IDs of playlists that
// directly contain it, using one query for all tracks. Tracks in no playlist
// have no entry.
func PlaylistIDs(ctx context.Context, q db.Querier, tracks ...*db.Track) (map[int64][]int64, error) {
	ids := make([]int64, 0, len(tracks))
	for _, t := range tracks {
		if t != nil {
			ids = append(ids, t.ID)
		}
	}
	memberships, err := db.PlaylistMemberships(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]int64, len(memberships))
	for id, playlists := range memberships {
		for _, p := range playlists {
			result[id] = append(result[id], p.ID)
		}
	}
	return result, nil
}

func playlistValue(p *playlist.PlaylistDetails, memberships map[int64][]int64) map[string]any {
	if p == nil {
		return nil
	}
	tracks := make([]map[string]any, 0, len(p.Tracks))
	for i := range p.Tracks {
		tracks = append(tracks, Track(&p.Tracks[i], memberships[p.Tracks[i].ID]))
	}
	return map[string]any{"id": p.ID, "title": p.Title, "parentId": p.ParentID, "trackCount": p.TrackCount, "totalDurationSeconds": p.TotalDuration, "tracks": tracks}
}
