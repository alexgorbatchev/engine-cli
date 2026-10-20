package db

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens an Engine DJ SQLite database.
func Open(path string, readonly ...bool) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("empty database path")
	}

	dsn := path
	if len(readonly) > 0 && readonly[0] {
		if strings.Contains(dsn, "?") {
			dsn += "&mode=ro"
		} else {
			dsn += "?mode=ro"
		}
	}

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}

	// Validate connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("pinging database %s: %w", path, err)
	}

	return database, nil
}

// InitSchema creates the necessary Engine DJ schema tables for synthetic tests.
func InitSchema(ctx context.Context, database *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS Information (id INTEGER PRIMARY KEY, uuid TEXT);
	INSERT OR IGNORE INTO Information(id,uuid) VALUES(1,'fixture');
	CREATE TABLE IF NOT EXISTS Track (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		playOrder INTEGER,
		length INTEGER,
		bpm INTEGER,
		year INTEGER,
		path TEXT,
		filename TEXT,
		bitrate INTEGER,
		bpmAnalyzed REAL,
		albumArtId INTEGER,
		fileBytes INTEGER,
		title TEXT,
		artist TEXT,
		album TEXT,
		genre TEXT,
		comment TEXT,
		label TEXT,
		composer TEXT,
		remixer TEXT,
		key INTEGER,
		rating INTEGER,
		albumArt TEXT,
		fileType TEXT,
		isAnalyzed BOOLEAN,
		isAvailable BOOLEAN,
		lastEditTime DATETIME,
		albumArtSourceHash CHAR(40),
		originDatabaseUuid TEXT,
		originTrackId INTEGER
	);
	CREATE TRIGGER IF NOT EXISTS trigger_after_insert_Track_fix_origin AFTER INSERT ON Track
	WHEN IFNULL(NEW.originTrackId,0)=0 OR IFNULL(NEW.originDatabaseUuid,'')=''
	BEGIN
		UPDATE Track SET originTrackId=NEW.id,originDatabaseUuid=(SELECT uuid FROM Information) WHERE id=NEW.id;
	END;

	CREATE TABLE IF NOT EXISTS AlbumArt (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT,
		albumArt BLOB
	);

	CREATE TABLE IF NOT EXISTS Playlist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT,
		parentListId INTEGER,
		isPersisted BOOLEAN,
		nextListId INTEGER,
		lastEditTime DATETIME,
		isExplicitlyExported BOOLEAN
	);

	CREATE TABLE IF NOT EXISTS PlaylistEntity (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		listId INTEGER,
		trackId INTEGER,
		databaseUuid TEXT,
		nextEntityId INTEGER,
		membershipReference INTEGER
	);
	`
	_, err := database.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}
	return nil
}

// GetAllTracks retrieves all tracks from the database.
func GetAllTracks(ctx context.Context, database *sql.DB) ([]Track, error) {
	query := `
		SELECT id, playOrder, length, bpm, year, path, filename, bitrate, bpmAnalyzed,
		       albumArtId, fileBytes, title, artist, album, genre, comment, label,
		       composer, remixer, key, rating, albumArt, fileType, isAnalyzed, isAvailable,
		       lastEditTime, albumArtSourceHash
		FROM Track
		ORDER BY id ASC;
	`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying tracks: %w", err)
	}
	defer rows.Close()

	var tracks []Track
	for rows.Next() {
		var t Track
		err := rows.Scan(
			&t.ID, &t.PlayOrder, &t.Length, &t.BPM, &t.Year, &t.Path, &t.Filename, &t.Bitrate, &t.BPMAnalyzed,
			&t.AlbumArtID, &t.FileBytes, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Comment, &t.Label,
			&t.Composer, &t.Remixer, &t.Key, &t.Rating, &t.AlbumArt, &t.FileType, &t.IsAnalyzed, &t.IsAvailable,
			&t.LastEditTime, &t.AlbumArtSourceHash,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning track: %w", err)
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tracks: %w", err)
	}

	return tracks, nil
}

// GetTrackByID retrieves a single track by its ID.
func GetTrackByID(ctx context.Context, database *sql.DB, id int64) (*Track, error) {
	query := `
		SELECT id, playOrder, length, bpm, year, path, filename, bitrate, bpmAnalyzed,
		       albumArtId, fileBytes, title, artist, album, genre, comment, label,
		       composer, remixer, key, rating, albumArt, fileType, isAnalyzed, isAvailable,
		       lastEditTime, albumArtSourceHash
		FROM Track
		WHERE id = ?;
	`
	row := database.QueryRowContext(ctx, query, id)
	var t Track
	err := row.Scan(
		&t.ID, &t.PlayOrder, &t.Length, &t.BPM, &t.Year, &t.Path, &t.Filename, &t.Bitrate, &t.BPMAnalyzed,
		&t.AlbumArtID, &t.FileBytes, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Comment, &t.Label,
		&t.Composer, &t.Remixer, &t.Key, &t.Rating, &t.AlbumArt, &t.FileType, &t.IsAnalyzed, &t.IsAvailable,
		&t.LastEditTime, &t.AlbumArtSourceHash,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting track %d: %w", id, err)
	}
	return &t, nil
}

// UpdateTrackMetadata updates a track's metadata, path, fileBytes, and lastEditTime.
func UpdateTrackMetadata(ctx context.Context, database *sql.DB, t *Track) error {
	nowISO := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	query := `
		UPDATE Track
		SET title = ?,
		    artist = ?,
		    album = ?,
		    genre = ?,
		    year = ?,
		    comment = ?,
		    path = ?,
		    filename = ?,
		    fileBytes = ?,
		    albumArtId = ?,
		    albumArt = ?,
		    albumArtSourceHash = ?,
		    lastEditTime = ?
		WHERE id = ?;
	`
	_, err := database.ExecContext(ctx, query,
		t.Title, t.Artist, t.Album, t.Genre, t.Year, t.Comment,
		t.Path, t.Filename, t.FileBytes, t.AlbumArtID, t.AlbumArt, t.AlbumArtSourceHash,
		nowISO, t.ID,
	)
	if err != nil {
		return fmt.Errorf("updating track %d: %w", t.ID, err)
	}
	return nil
}

// GetAllPlaylists retrieves all playlists and folders from the database.
func GetAllPlaylists(ctx context.Context, database *sql.DB) ([]Playlist, error) {
	query := `
		SELECT id, title, parentListId
		FROM Playlist
		ORDER BY parentListId ASC, id ASC;
	`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying playlists: %w", err)
	}
	defer rows.Close()

	var playlists []Playlist
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Title, &p.ParentListID); err != nil {
			return nil, fmt.Errorf("scanning playlist: %w", err)
		}
		playlists = append(playlists, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating playlists: %w", err)
	}

	return playlists, nil
}

// GetPlaylistTracks retrieves all tracks that belong to a specific playlist ID.
func GetPlaylistTracks(ctx context.Context, database *sql.DB, listID int64) ([]Track, error) {
	query := `
		SELECT t.id, t.playOrder, t.length, t.bpm, t.year, t.path, t.filename, t.bitrate, t.bpmAnalyzed,
		       t.albumArtId, t.fileBytes, t.title, t.artist, t.album, t.genre, t.comment, t.label,
		       t.composer, t.remixer, t.key, t.rating, t.albumArt, t.fileType, t.isAnalyzed, t.isAvailable,
		       t.lastEditTime, t.albumArtSourceHash
		FROM PlaylistEntity pe
		JOIN Track t ON t.originTrackId = pe.trackId AND t.originDatabaseUuid = pe.databaseUuid
		WHERE pe.listId = ?
		ORDER BY pe.id ASC;
	`
	rows, err := database.QueryContext(ctx, query, listID)
	if err != nil {
		return nil, fmt.Errorf("querying playlist tracks for list %d: %w", listID, err)
	}
	defer rows.Close()

	var tracks []Track
	for rows.Next() {
		var t Track
		err := rows.Scan(
			&t.ID, &t.PlayOrder, &t.Length, &t.BPM, &t.Year, &t.Path, &t.Filename, &t.Bitrate, &t.BPMAnalyzed,
			&t.AlbumArtID, &t.FileBytes, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Comment, &t.Label,
			&t.Composer, &t.Remixer, &t.Key, &t.Rating, &t.AlbumArt, &t.FileType, &t.IsAnalyzed, &t.IsAvailable,
			&t.LastEditTime, &t.AlbumArtSourceHash,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning playlist track: %w", err)
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating playlist tracks: %w", err)
	}

	return tracks, nil
}

// GetAllAlbumArt retrieves all entries from the AlbumArt table.
func GetAllAlbumArt(ctx context.Context, database *sql.DB) ([]AlbumArtRecord, error) {
	query := `
		SELECT id, hash, albumArt
		FROM AlbumArt
		ORDER BY id ASC;
	`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying album art: %w", err)
	}
	defer rows.Close()

	var records []AlbumArtRecord
	for rows.Next() {
		var r AlbumArtRecord
		var hashNull sql.NullString
		if err := rows.Scan(&r.ID, &hashNull, &r.AlbumArt); err != nil {
			return nil, fmt.Errorf("scanning album art: %w", err)
		}
		r.Hash = hashNull.String
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating album art: %w", err)
	}

	return records, nil
}

// UpdateAlbumArtByID updates an existing AlbumArt row directly by its ID.
func UpdateAlbumArtByID(ctx context.Context, database *sql.DB, id int64, hash string, blob []byte) error {
	_, err := database.ExecContext(ctx, "UPDATE AlbumArt SET hash = ?, albumArt = ? WHERE id = ?", hash, blob, id)
	if err != nil {
		return fmt.Errorf("updating album art %d: %w", id, err)
	}
	return nil
}

// InsertOrUpdateAlbumArt inserts or updates an album art record by hash.
func InsertOrUpdateAlbumArt(ctx context.Context, database *sql.DB, hash string, blob []byte) (int64, error) {
	var existingID int64
	err := database.QueryRowContext(ctx, "SELECT id FROM AlbumArt WHERE hash = ?", hash).Scan(&existingID)
	if err == nil {
		// Update existing blob if needed
		_, updateErr := database.ExecContext(ctx, "UPDATE AlbumArt SET albumArt = ? WHERE id = ?", blob, existingID)
		if updateErr != nil {
			return 0, fmt.Errorf("updating album art %d: %w", existingID, updateErr)
		}
		return existingID, nil
	} else if err != sql.ErrNoRows {
		return 0, fmt.Errorf("checking album art hash: %w", err)
	}

	// Insert new
	res, insertErr := database.ExecContext(ctx, "INSERT INTO AlbumArt (hash, albumArt) VALUES (?, ?)", hash, blob)
	if insertErr != nil {
		return 0, fmt.Errorf("inserting album art: %w", insertErr)
	}
	id, errID := res.LastInsertId()
	if errID != nil {
		return 0, fmt.Errorf("getting inserted album art id: %w", errID)
	}
	return id, nil
}

// FindPlaylist finds a playlist by library-scoped numeric ID.
func FindPlaylist(ctx context.Context, database *sql.DB, idOrTitle string) (*Playlist, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	idOrTitle = strings.TrimSpace(idOrTitle)
	if strings.HasPrefix(idOrTitle, "eng:") {
		id, err := identity.ResolveDatabase(ctx, database, "playlist", idOrTitle)
		if err != nil {
			return nil, err
		}
		idOrTitle = strconv.FormatInt(id, 10)
	}
	if idOrTitle == "" {
		return nil, fmt.Errorf("empty playlist identifier")
	}

	// Try numeric ID first
	if id, err := strconv.ParseInt(idOrTitle, 10, 64); err == nil && id > 0 {
		var p Playlist
		err := database.QueryRowContext(ctx, "SELECT id, title, parentListId FROM Playlist WHERE id = ?", id).
			Scan(&p.ID, &p.Title, &p.ParentListID)
		if err == nil {
			return &p, nil
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("querying playlist by id %d: %w", id, err)
		}
	}

	if id, err := strconv.ParseInt(idOrTitle, 10, 64); err != nil || id <= 0 {
		return nil, fmt.Errorf("playlist identifier must be a positive numeric ID; list playlists to choose an ID")
	}

	return nil, nil
}

// CreatePlaylist creates a new playlist or folder in the Playlist table.
func CreatePlaylist(ctx context.Context, database *sql.DB, title string, parentListID int64, isFolder bool) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("database cannot be nil")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return 0, fmt.Errorf("playlist title cannot be empty")
	}

	if parentListID > 0 {
		var parentExists bool
		err := database.QueryRowContext(ctx, "SELECT 1 FROM Playlist WHERE id = ?", parentListID).Scan(&parentExists)
		if err != nil {
			if err == sql.ErrNoRows {
				return 0, fmt.Errorf("parent playlist ID %d does not exist", parentListID)
			}
			return 0, fmt.Errorf("checking parent playlist: %w", err)
		}
	}

	// Engine uses the same persisted row for playlists and folders. Its insert
	// triggers link the new tail (nextListId=0) into the parent's sibling chain.
	query := "INSERT INTO Playlist (title, parentListId, isPersisted, nextListId, lastEditTime, isExplicitlyExported) VALUES (?, ?, 1, 0, ?, 1)"
	res, err := database.ExecContext(ctx, query, title, parentListID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("creating playlist %q: %w", title, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting inserted playlist id: %w", err)
	}

	return id, nil
}

// DeletePlaylist deletes a playlist and its associated PlaylistEntity records.
// If the playlist has children and force is false, it returns an error.
func DeletePlaylist(ctx context.Context, database *sql.DB, id int64, force bool) error {
	if database == nil {
		return fmt.Errorf("database cannot be nil")
	}

	var exists bool
	err := database.QueryRowContext(ctx, "SELECT 1 FROM Playlist WHERE id = ?", id).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("playlist ID %d not found", id)
		}
		return fmt.Errorf("checking playlist %d: %w", id, err)
	}

	var childCount int
	err = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM Playlist WHERE parentListId = ?", id).Scan(&childCount)
	if err != nil {
		return fmt.Errorf("checking child playlists for %d: %w", id, err)
	}

	if childCount > 0 && !force {
		return fmt.Errorf("playlist ID %d has %d child playlist(s); use --force to delete recursively", id, childCount)
	}

	// Helper to recursively collect all descendant IDs
	var collectDescendants func(parent int64) ([]int64, error)
	collectDescendants = func(parent int64) ([]int64, error) {
		rows, err := database.QueryContext(ctx, "SELECT id FROM Playlist WHERE parentListId = ?", parent)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var ids []int64
		for rows.Next() {
			var childID int64
			if err := rows.Scan(&childID); err != nil {
				return nil, err
			}
			ids = append(ids, childID)
			subIDs, err := collectDescendants(childID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, subIDs...)
		}
		return ids, rows.Err()
	}

	allToDelete := []int64{id}
	if childCount > 0 {
		descendants, err := collectDescendants(id)
		if err != nil {
			return fmt.Errorf("collecting descendant playlists: %w", err)
		}
		allToDelete = append(allToDelete, descendants...)
	}

	for _, delID := range allToDelete {
		if _, err := database.ExecContext(ctx, "DELETE FROM PlaylistEntity WHERE listId = ?", delID); err != nil {
			return fmt.Errorf("deleting playlist entities for %d: %w", delID, err)
		}
		if _, err := database.ExecContext(ctx, "DELETE FROM Playlist WHERE id = ?", delID); err != nil {
			return fmt.Errorf("deleting playlist %d: %w", delID, err)
		}
	}

	return nil
}

// MovePlaylist updates the parentListId of a playlist/folder.
func MovePlaylist(ctx context.Context, database *sql.DB, id int64, newParentID int64) error {
	if database == nil {
		return fmt.Errorf("database cannot be nil")
	}
	if id == newParentID {
		return fmt.Errorf("cannot move playlist to be its own parent")
	}

	var exists bool
	err := database.QueryRowContext(ctx, "SELECT 1 FROM Playlist WHERE id = ?", id).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("playlist ID %d not found", id)
		}
		return fmt.Errorf("checking playlist %d: %w", id, err)
	}

	if newParentID > 0 {
		var parentExists bool
		err := database.QueryRowContext(ctx, "SELECT 1 FROM Playlist WHERE id = ?", newParentID).Scan(&parentExists)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("destination parent playlist ID %d not found", newParentID)
			}
			return fmt.Errorf("checking destination parent %d: %w", newParentID, err)
		}

		// Prevent circular hierarchy
		curr := newParentID
		for curr > 0 {
			if curr == id {
				return fmt.Errorf("cannot move playlist %d inside its own child playlist %d", id, newParentID)
			}
			var nextParent int64
			err := database.QueryRowContext(ctx, "SELECT parentListId FROM Playlist WHERE id = ?", curr).Scan(&nextParent)
			if err != nil {
				break
			}
			curr = nextParent
		}
	}

	_, err = database.ExecContext(ctx, "UPDATE Playlist SET parentListId = ? WHERE id = ?", newParentID, id)
	if err != nil {
		return fmt.Errorf("updating playlist %d parent: %w", id, err)
	}

	return nil
}

// AddTrackToPlaylist adds a track to a playlist, maintaining the linked list in PlaylistEntity.
func AddTrackToPlaylist(ctx context.Context, database *sql.DB, listID int64, trackID int64) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("database cannot be nil")
	}

	var pExists bool
	err := database.QueryRowContext(ctx, "SELECT 1 FROM Playlist WHERE id = ?", listID).Scan(&pExists)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("playlist ID %d not found", listID)
		}
		return 0, fmt.Errorf("checking playlist %d: %w", listID, err)
	}

	var originID int64
	var originUUID string
	err = database.QueryRowContext(ctx, "SELECT originTrackId, originDatabaseUuid FROM Track WHERE id = ?", trackID).Scan(&originID, &originUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("track ID %d not found", trackID)
		}
		return 0, fmt.Errorf("checking track %d: %w", trackID, err)
	}

	// Check if already in playlist
	var existingEntityID int64
	err = database.QueryRowContext(ctx, "SELECT id FROM PlaylistEntity WHERE listId = ? AND trackId = ? AND databaseUuid = ?", listID, originID, originUUID).Scan(&existingEntityID)
	if err == nil {
		return existingEntityID, nil // already present
	} else if err != sql.ErrNoRows {
		return 0, fmt.Errorf("checking existing playlist entity: %w", err)
	}

	// Find the current tail entity (where nextEntityId = 0 or last entity by id)
	var tailID int64
	_ = database.QueryRowContext(ctx, "SELECT id FROM PlaylistEntity WHERE listId = ? AND (nextEntityId = 0 OR nextEntityId IS NULL) ORDER BY id DESC LIMIT 1", listID).Scan(&tailID)

	res, err := database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId, databaseUuid, nextEntityId, membershipReference) VALUES (?, ?, ?, 0, 0)", listID, originID, originUUID)
	if err != nil {
		return 0, fmt.Errorf("inserting playlist entity: %w", err)
	}

	newID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting inserted entity id: %w", err)
	}

	if tailID > 0 && tailID != newID {
		_, _ = database.ExecContext(ctx, "UPDATE PlaylistEntity SET nextEntityId = ? WHERE id = ?", newID, tailID)
	}

	return newID, nil
}

// RemoveTrackFromPlaylist removes a track from a playlist and relinks adjacent PlaylistEntities.
func RemoveTrackFromPlaylist(ctx context.Context, database *sql.DB, listID int64, trackID int64) error {
	if database == nil {
		return fmt.Errorf("database cannot be nil")
	}

	var entityID int64
	var nextEntityID sql.NullInt64
	err := database.QueryRowContext(ctx, `SELECT pe.id, pe.nextEntityId FROM PlaylistEntity pe
	JOIN Track t ON t.originTrackId=pe.trackId AND t.originDatabaseUuid=pe.databaseUuid
	WHERE pe.listId=? AND t.id=?`, listID, trackID).Scan(&entityID, &nextEntityID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("track ID %d not found in playlist %d", trackID, listID)
		}
		return fmt.Errorf("checking track in playlist: %w", err)
	}

	// Relink previous node to nextEntityID
	var targetNext int64
	if nextEntityID.Valid {
		targetNext = nextEntityID.Int64
	}
	_, _ = database.ExecContext(ctx, "UPDATE PlaylistEntity SET nextEntityId = ? WHERE listId = ? AND nextEntityId = ?", targetNext, listID, entityID)

	// Delete target entity
	_, err = database.ExecContext(ctx, "DELETE FROM PlaylistEntity WHERE id = ?", entityID)
	if err != nil {
		return fmt.Errorf("deleting playlist entity %d: %w", entityID, err)
	}

	return nil
}

// MoveTrackInPlaylist moves a track membership from srcListID to dstListID.
func MoveTrackInPlaylist(ctx context.Context, database *sql.DB, srcListID int64, dstListID int64, trackID int64) error {
	if srcListID == dstListID {
		return nil
	}
	if err := RemoveTrackFromPlaylist(ctx, database, srcListID, trackID); err != nil {
		return fmt.Errorf("removing from source playlist: %w", err)
	}
	if _, err := AddTrackToPlaylist(ctx, database, dstListID, trackID); err != nil {
		return fmt.Errorf("adding to destination playlist: %w", err)
	}
	return nil
}

// InsertTrack inserts a new Track record into the Track table and returns its ID.
func InsertTrack(ctx context.Context, database *sql.DB, t *Track) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("database cannot be nil")
	}
	nowISO := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	t.LastEditTime = sql.NullString{String: nowISO, Valid: true}

	query := `
		INSERT INTO Track (
			playOrder, length, bpm, year, path, filename, bitrate, bpmAnalyzed,
			albumArtId, fileBytes, title, artist, album, genre, comment, label,
			composer, remixer, key, rating, albumArt, fileType, isAnalyzed, isAvailable,
			lastEditTime, albumArtSourceHash
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?
		);
	`
	res, err := database.ExecContext(ctx, query,
		t.PlayOrder, t.Length, t.BPM, t.Year, t.Path, t.Filename, t.Bitrate, t.BPMAnalyzed,
		t.AlbumArtID, t.FileBytes, t.Title, t.Artist, t.Album, t.Genre, t.Comment, t.Label,
		t.Composer, t.Remixer, t.Key, t.Rating, t.AlbumArt, t.FileType, t.IsAnalyzed, t.IsAvailable,
		t.LastEditTime, t.AlbumArtSourceHash,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting track: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting inserted track id: %w", err)
	}
	t.ID = id
	return id, nil
}

// DeleteTrack deletes a track from the Track table and removes all its PlaylistEntity references.
func DeleteTrack(ctx context.Context, database *sql.DB, id int64) error {
	if database == nil {
		return fmt.Errorf("database cannot be nil")
	}

	var exists bool
	err := database.QueryRowContext(ctx, "SELECT 1 FROM Track WHERE id = ?", id).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("track ID %d not found", id)
		}
		return fmt.Errorf("checking track %d: %w", id, err)
	}

	if _, err := database.ExecContext(ctx, `DELETE FROM PlaylistEntity WHERE (trackId,databaseUuid) IN
	(SELECT originTrackId,originDatabaseUuid FROM Track WHERE id=?)`, id); err != nil {
		return fmt.Errorf("deleting playlist entities for track %d: %w", id, err)
	}

	if _, err := database.ExecContext(ctx, "DELETE FROM Track WHERE id = ?", id); err != nil {
		return fmt.Errorf("deleting track %d: %w", id, err)
	}

	return nil
}

// FindTrack finds a track by numeric ID or its exact stored path.
func FindTrack(ctx context.Context, database *sql.DB, idOrPath string) (*Track, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	idOrPath = strings.TrimSpace(idOrPath)
	if strings.HasPrefix(idOrPath, "eng:") {
		id, err := identity.ResolveDatabase(ctx, database, "track", idOrPath)
		if err != nil {
			return nil, err
		}
		return GetTrackByID(ctx, database, id)
	}
	if idOrPath == "" {
		return nil, fmt.Errorf("empty track identifier")
	}

	// Try numeric ID first
	if id, err := strconv.ParseInt(idOrPath, 10, 64); err == nil && id > 0 {
		t, err := GetTrackByID(ctx, database, id)
		if err != nil {
			return nil, err
		}
		return t, nil
	}

	// Paths match exactly; titles, basenames, and path prefixes are not selectors.
	query := `
		SELECT id, playOrder, length, bpm, year, path, filename, bitrate, bpmAnalyzed,
		       albumArtId, fileBytes, title, artist, album, genre, comment, label,
		       composer, remixer, key, rating, albumArt, fileType, isAnalyzed, isAvailable,
		       lastEditTime, albumArtSourceHash
		FROM Track
		WHERE path = ?;
	`
	row := database.QueryRowContext(ctx, query, idOrPath)
	var t Track
	err := row.Scan(
		&t.ID, &t.PlayOrder, &t.Length, &t.BPM, &t.Year, &t.Path, &t.Filename, &t.Bitrate, &t.BPMAnalyzed,
		&t.AlbumArtID, &t.FileBytes, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Comment, &t.Label,
		&t.Composer, &t.Remixer, &t.Key, &t.Rating, &t.AlbumArt, &t.FileType, &t.IsAnalyzed, &t.IsAvailable,
		&t.LastEditTime, &t.AlbumArtSourceHash,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("finding track %q: %w", idOrPath, err)
	}
	return &t, nil
}

// RelocateTracks replaces oldPrefix with newPrefix in Track.path for matching tracks.
func RelocateTracks(ctx context.Context, database *sql.DB, oldPrefix string, newPrefix string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("database cannot be nil")
	}
	if oldPrefix == "" {
		return 0, fmt.Errorf("old prefix cannot be empty")
	}

	tracks, err := GetAllTracks(ctx, database)
	if err != nil {
		return 0, fmt.Errorf("fetching tracks for relocation: %w", err)
	}

	relocatedCount := 0
	for _, t := range tracks {
		pathStr := t.PathString()
		if strings.HasPrefix(pathStr, oldPrefix) || strings.Contains(pathStr, oldPrefix) {
			newPath := strings.Replace(pathStr, oldPrefix, newPrefix, 1)
			newFilename := filepath.Base(newPath)
			t.Path = sql.NullString{String: newPath, Valid: true}
			t.Filename = sql.NullString{String: newFilename, Valid: true}
			if err := UpdateTrackMetadata(ctx, database, &t); err != nil {
				return relocatedCount, fmt.Errorf("updating relocated track %d: %w", t.ID, err)
			}
			relocatedCount++
		}
	}

	return relocatedCount, nil
}

// DeleteAlbumArt removes an AlbumArt row by ID.
func DeleteAlbumArt(ctx context.Context, database *sql.DB, id int64) error {
	_, err := database.ExecContext(ctx, "DELETE FROM AlbumArt WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting album art %d: %w", id, err)
	}
	return nil
}
