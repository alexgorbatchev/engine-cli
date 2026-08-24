package db

import (
	"context"
	"database/sql"
	"fmt"
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
		albumArtSourceHash CHAR(40)
	);

	CREATE TABLE IF NOT EXISTS AlbumArt (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT,
		albumArt BLOB
	);

	CREATE TABLE IF NOT EXISTS Playlist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT,
		parentListId INTEGER,
		isFolder BOOLEAN
	);

	CREATE TABLE IF NOT EXISTS PlaylistEntity (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		listId INTEGER,
		trackId INTEGER,
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
		JOIN Track t ON t.id = pe.trackId
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

// DeleteAlbumArt removes an AlbumArt row by ID.
func DeleteAlbumArt(ctx context.Context, database *sql.DB, id int64) error {
	_, err := database.ExecContext(ctx, "DELETE FROM AlbumArt WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting album art %d: %w", id, err)
	}
	return nil
}
