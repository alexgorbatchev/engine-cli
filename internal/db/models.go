package db

import "database/sql"

// Track represents an audio track record in Engine DJ's Track table.
type Track struct {
	ID                 int64           `json:"id"`
	PlayOrder          sql.NullInt64   `json:"playOrder"`
	Length             sql.NullInt64   `json:"length"`
	BPM                sql.NullInt64   `json:"bpm"`
	Year               sql.NullInt64   `json:"year"`
	Path               sql.NullString  `json:"path"`
	Filename           sql.NullString  `json:"filename"`
	Bitrate            sql.NullInt64   `json:"bitrate"`
	BPMAnalyzed        sql.NullFloat64 `json:"bpmAnalyzed"`
	AlbumArtID         sql.NullInt64   `json:"albumArtId"`
	HasArtwork         sql.NullBool    `json:"-"`
	FileBytes          sql.NullInt64   `json:"fileBytes"`
	Title              sql.NullString  `json:"title"`
	Artist             sql.NullString  `json:"artist"`
	Album              sql.NullString  `json:"album"`
	Genre              sql.NullString  `json:"genre"`
	Comment            sql.NullString  `json:"comment"`
	Label              sql.NullString  `json:"label"`
	Composer           sql.NullString  `json:"composer"`
	Remixer            sql.NullString  `json:"remixer"`
	Key                sql.NullInt64   `json:"key"`
	Rating             sql.NullInt64   `json:"rating"`
	AlbumArt           sql.NullString  `json:"albumArt"`
	FileType           sql.NullString  `json:"fileType"`
	IsAnalyzed         sql.NullBool    `json:"isAnalyzed"`
	IsAvailable        sql.NullBool    `json:"isAvailable"`
	LastEditTime       sql.NullString  `json:"lastEditTime"`
	AlbumArtSourceHash sql.NullString  `json:"albumArtSourceHash"`
}

// PathString returns the track's path or empty string if null.
func (t *Track) PathString() string {
	if t.Path.Valid {
		return t.Path.String
	}
	return ""
}

// FilenameString returns the track's filename or empty string if null.
func (t *Track) FilenameString() string {
	if t.Filename.Valid {
		return t.Filename.String
	}
	return ""
}

// AlbumArtRecord represents an entry in the AlbumArt table.
type AlbumArtRecord struct {
	ID       int64  `json:"id"`
	Hash     string `json:"hash"`     // SHA-1 hex hash
	AlbumArt []byte `json:"albumArt"` // PNG/JPEG binary BLOB
}

// Playlist represents a playlist or folder in the Playlist table.
type Playlist struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	ParentListID int64  `json:"parentListId"`
	IsFolder     bool   `json:"isFolder,omitempty"`
}

// PlaylistEntity represents a track association in the PlaylistEntity table.
type PlaylistEntity struct {
	ID                  int64 `json:"id"`
	ListID              int64 `json:"listId"`
	TrackID             int64 `json:"trackId"`
	NextEntityID        int64 `json:"nextEntityId"`
	MembershipReference int64 `json:"membershipReference"`
}
