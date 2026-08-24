package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func createTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "m.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	ctx := context.Background()
	if err := InitSchema(ctx, database); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	t.Cleanup(func() {
		_ = database.Close()
	})

	return database, dbPath
}

func TestOpen_ErrorsAndReadonly(t *testing.T) {
	// 1. Empty path error
	_, err := Open("")
	if err == nil {
		t.Error("expected error for empty database path")
	}

	// 2. Readonly mode
	_, dbPath := createTestDB(t)
	roDB, err := Open(dbPath, true)
	if err != nil {
		t.Fatalf("Open in readonly failed: %v", err)
	}
	defer roDB.Close()

	roDB2, err := Open(dbPath+"?cache=shared", true)
	if err != nil {
		t.Fatalf("Open with query in readonly failed: %v", err)
	}
	defer roDB2.Close()

	// 3. Ping error on invalid directory/path
	_, err = Open(filepath.Join(t.TempDir(), "nonexistent", "sub", "m.db"))
	if err == nil {
		t.Error("expected error for unopenable database path")
	}
}

func TestTrackCRUD(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// Insert sample track
	insertQuery := `
		INSERT INTO Track (path, filename, fileBytes, title, artist, album, genre, year, albumArtId)
		VALUES ('Music/Artist/Album/track.m4a', 'track.m4a', 5000, 'Test Title', 'Test Artist', 'Test Album', 'House', 2024, 1);
	`
	res, err := database.ExecContext(ctx, insertQuery)
	if err != nil {
		t.Fatalf("inserting track: %v", err)
	}
	trackID, _ := res.LastInsertId()

	// 1. GetAllTracks
	tracks, err := GetAllTracks(ctx, database)
	if err != nil {
		t.Fatalf("GetAllTracks error: %v", err)
	}
	if len(tracks) != 1 || tracks[0].ID != trackID {
		t.Fatalf("expected 1 track with id %d, got %v", trackID, tracks)
	}
	if tracks[0].Title.String != "Test Title" {
		t.Errorf("expected Title 'Test Title', got %q", tracks[0].Title.String)
	}

	// 2. GetTrackByID
	tFound, err := GetTrackByID(ctx, database, trackID)
	if err != nil {
		t.Fatalf("GetTrackByID error: %v", err)
	}
	if tFound == nil || tFound.ID != trackID {
		t.Fatalf("expected track %d, got %v", trackID, tFound)
	}

	// Non-existent ID returns nil, nil
	tNotFound, err := GetTrackByID(ctx, database, 99999)
	if err != nil || tNotFound != nil {
		t.Fatalf("expected nil for nonexistent ID, got %v, err=%v", tNotFound, err)
	}

	// 3. UpdateTrackMetadata
	tFound.Title = sql.NullString{String: "Updated Title", Valid: true}
	tFound.Artist = sql.NullString{String: "Updated Artist", Valid: true}
	tFound.FileBytes = sql.NullInt64{Int64: 6000, Valid: true}

	err = UpdateTrackMetadata(ctx, database, tFound)
	if err != nil {
		t.Fatalf("UpdateTrackMetadata error: %v", err)
	}

	tUpdated, _ := GetTrackByID(ctx, database, trackID)
	if tUpdated.Title.String != "Updated Title" || tUpdated.FileBytes.Int64 != 6000 {
		t.Errorf("track not updated properly: %v", tUpdated)
	}
}

func TestPlaylistAndEntities(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// Insert playlists (Folder + Child Playlist)
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId, isFolder) VALUES (10, 'EDM', 0, 1)")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId, isFolder) VALUES (11, 'House', 10, 0)")

	// Insert tracks
	res1, _ := database.ExecContext(ctx, "INSERT INTO Track (title, artist, path, filename) VALUES ('T1', 'A1', 'p1', 'f1')")
	id1, _ := res1.LastInsertId()
	res2, _ := database.ExecContext(ctx, "INSERT INTO Track (title, artist, path, filename) VALUES ('T2', 'A2', 'p2', 'f2')")
	id2, _ := res2.LastInsertId()

	// Link tracks to playlist 11
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (11, ?)", id1)
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (11, ?)", id2)

	// 1. GetAllPlaylists
	pls, err := GetAllPlaylists(ctx, database)
	if err != nil {
		t.Fatalf("GetAllPlaylists error: %v", err)
	}
	if len(pls) != 2 {
		t.Fatalf("expected 2 playlists, got %d", len(pls))
	}
	if pls[0].Title != "EDM" || pls[1].Title != "House" {
		t.Errorf("unexpected playlists: %v", pls)
	}

	// 2. GetPlaylistTracks
	plTracks, err := GetPlaylistTracks(ctx, database, 11)
	if err != nil {
		t.Fatalf("GetPlaylistTracks error: %v", err)
	}
	if len(plTracks) != 2 {
		t.Fatalf("expected 2 playlist tracks, got %d", len(plTracks))
	}
	if plTracks[0].ID != id1 || plTracks[1].ID != id2 {
		t.Errorf("unexpected playlist track IDs: %v", plTracks)
	}
}

func TestAlbumArtOperations(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	blob := []byte("fake png image data")
	hash := "a1b2c3d4e5f67890123456789012345678901234"

	// 1. Insert
	id, err := InsertOrUpdateAlbumArt(ctx, database, hash, blob)
	if err != nil {
		t.Fatalf("InsertOrUpdateAlbumArt error: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	// 2. Update existing by hash
	newBlob := []byte("updated fake png data")
	id2, err := InsertOrUpdateAlbumArt(ctx, database, hash, newBlob)
	if err != nil {
		t.Fatalf("update AlbumArt error: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same ID %d, got %d", id, id2)
	}

	// Insert second art
	hash2 := "b2c3d4e5f6789012345678901234567890123456"
	_, err = InsertOrUpdateAlbumArt(ctx, database, hash2, blob)
	if err != nil {
		t.Fatalf("insert second AlbumArt error: %v", err)
	}

	// 3. GetAllAlbumArt
	records, err := GetAllAlbumArt(ctx, database)
	if err != nil {
		t.Fatalf("GetAllAlbumArt error: %v", err)
	}
	if len(records) != 2 || string(records[0].AlbumArt) != string(newBlob) {
		t.Fatalf("unexpected AlbumArt records: %v", records)
	}

	// 3b. UpdateAlbumArtByID
	err = UpdateAlbumArtByID(ctx, database, id, "updated_hash", []byte("direct update blob"))
	if err != nil {
		t.Fatalf("UpdateAlbumArtByID error: %v", err)
	}

	// 4. DeleteAlbumArt
	err = DeleteAlbumArt(ctx, database, id)
	if err != nil {
		t.Fatalf("DeleteAlbumArt error: %v", err)
	}
	err = DeleteAlbumArt(ctx, database, 2)
	if err != nil {
		t.Fatalf("DeleteAlbumArt 2 error: %v", err)
	}
	recordsAfter, _ := GetAllAlbumArt(ctx, database)
	if len(recordsAfter) != 0 {
		t.Errorf("expected 0 records after delete, got %d", len(recordsAfter))
	}
}

func TestTrack_PathAndFilenameString(t *testing.T) {
	trEmpty := &Track{}
	if trEmpty.PathString() != "" || trEmpty.FilenameString() != "" {
		t.Errorf("expected empty string for null path/filename, got path=%q, fn=%q", trEmpty.PathString(), trEmpty.FilenameString())
	}

	trValid := &Track{
		Path:     sql.NullString{String: "Music/Artist/Track.m4a", Valid: true},
		Filename: sql.NullString{String: "Track.m4a", Valid: true},
	}
	if trValid.PathString() != "Music/Artist/Track.m4a" || trValid.FilenameString() != "Track.m4a" {
		t.Errorf("unexpected path/filename strings: path=%q, fn=%q", trValid.PathString(), trValid.FilenameString())
	}
}

func TestDB_ClosedErrors(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()
	_ = database.Close()

	if err := InitSchema(ctx, database); err == nil {
		t.Error("expected error on closed db InitSchema")
	}
	if _, err := GetAllTracks(ctx, database); err == nil {
		t.Error("expected error on closed db GetAllTracks")
	}
	if _, err := GetTrackByID(ctx, database, 1); err == nil {
		t.Error("expected error on closed db GetTrackByID")
	}
	if err := UpdateTrackMetadata(ctx, database, &Track{ID: 1}); err == nil {
		t.Error("expected error on closed db UpdateTrackMetadata")
	}
	if _, err := GetAllPlaylists(ctx, database); err == nil {
		t.Error("expected error on closed db GetAllPlaylists")
	}
	if _, err := GetPlaylistTracks(ctx, database, 1); err == nil {
		t.Error("expected error on closed db GetPlaylistTracks")
	}
	if _, err := GetAllAlbumArt(ctx, database); err == nil {
		t.Error("expected error on closed db GetAllAlbumArt")
	}
	if _, err := InsertOrUpdateAlbumArt(ctx, database, "h", []byte{1}); err == nil {
		t.Error("expected error on closed db InsertOrUpdateAlbumArt")
	}
	if err := DeleteAlbumArt(ctx, database, 1); err == nil {
		t.Error("expected error on closed db DeleteAlbumArt")
	}
	if err := UpdateAlbumArtByID(ctx, database, 1, "h", []byte{1}); err == nil {
		t.Error("expected error on closed db UpdateAlbumArtByID")
	}
}
