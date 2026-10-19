package details

import (
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/playlist"
	"github.com/dj/engine-cli/internal/track"
)

func valueOrNull[T any](value T, valid bool) any {
	if !valid {
		return nil
	}
	return value
}

// HTTP consumers receive JSON values and nulls, not database/sql wrappers.
func Track(t *db.Track) map[string]any {
	if t == nil {
		return nil
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
	}
}

func Playlist(p *playlist.PlaylistDetails) map[string]any {
	if p == nil {
		return nil
	}
	tracks := make([]map[string]any, 0, len(p.Tracks))
	for _, t := range p.Tracks {
		tracks = append(tracks, Track(&t))
	}
	return map[string]any{"id": p.ID, "title": p.Title, "parentId": p.ParentID, "trackCount": p.TrackCount, "totalDurationSeconds": p.TotalDuration, "tracks": tracks}
}
