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

	specialPath := filepath.Join(t.TempDir(), "library #100%.db")
	specialDB, err := Open(specialPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = specialDB.Close()
	roDB2, err := Open(specialPath, true)
	if err != nil {
		t.Fatalf("Open literal special-character path in readonly failed: %v", err)
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
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (10, 'EDM', 0)")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (11, 'House', 10)")

	// Insert tracks
	res1, _ := database.ExecContext(ctx, "INSERT INTO Track (title, artist, path, filename) VALUES ('T1', 'A1', 'p1', 'f1')")
	id1, _ := res1.LastInsertId()
	res2, _ := database.ExecContext(ctx, "INSERT INTO Track (title, artist, path, filename) VALUES ('T2', 'A2', 'p2', 'f2')")
	id2, _ := res2.LastInsertId()

	// Link tracks to playlist 11
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId, databaseUuid) VALUES (11, ?, 'fixture')", id1)
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId, databaseUuid) VALUES (11, ?, 'fixture')", id2)

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
	if _, err := FindPlaylist(ctx, database, "House"); err == nil {
		t.Error("expected error on closed db FindPlaylist")
	}
	if _, err := CreatePlaylist(ctx, database, "House", 0, false); err == nil {
		t.Error("expected error on closed db CreatePlaylist")
	}
	if err := DeletePlaylist(ctx, database, 1, false); err == nil {
		t.Error("expected error on closed db DeletePlaylist")
	}
	if err := MovePlaylist(ctx, database, 1, 2); err == nil {
		t.Error("expected error on closed db MovePlaylist")
	}
	if _, err := AddTrackToPlaylist(ctx, database, 1, 1); err == nil {
		t.Error("expected error on closed db AddTrackToPlaylist")
	}
	if err := RemoveTrackFromPlaylist(ctx, database, 1, 1); err == nil {
		t.Error("expected error on closed db RemoveTrackFromPlaylist")
	}
	if _, err := InsertTrack(ctx, database, &Track{}); err == nil {
		t.Error("expected error on closed db InsertTrack")
	}
	if err := DeleteTrack(ctx, database, 1); err == nil {
		t.Error("expected error on closed db DeleteTrack")
	}
	if _, err := FindTrack(ctx, database, "track"); err == nil {
		t.Error("expected error on closed db FindTrack")
	}
	if _, err := RelocateTracks(ctx, database, "old", "new"); err == nil {
		t.Error("expected error on closed db RelocateTracks")
	}
}

func TestPlaylistCRUDAndHierarchy(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// 1. Create root playlist
	pID1, err := CreatePlaylist(ctx, database, "EDM", 0, true)
	if err != nil {
		t.Fatalf("CreatePlaylist error: %v", err)
	}
	if pID1 <= 0 {
		t.Fatalf("expected positive ID, got %d", pID1)
	}

	// 2. Create child playlist
	pID2, err := CreatePlaylist(ctx, database, "Tech House", pID1, false)
	if err != nil {
		t.Fatalf("CreatePlaylist child error: %v", err)
	}

	// 3. FindPlaylist by ID and Name
	pFoundID, err := FindPlaylist(ctx, database, "1")
	if err != nil || pFoundID == nil || pFoundID.Title != "EDM" {
		t.Fatalf("FindPlaylist by ID error: %v, got %v", err, pFoundID)
	}

	pFoundName, err := FindPlaylist(ctx, database, "2")
	if err != nil || pFoundName == nil || pFoundName.ID != pID2 {
		t.Fatalf("FindPlaylist by name error: %v, got %v", err, pFoundName)
	}

	pNotFoundNum, err := FindPlaylist(ctx, database, "99999")
	if err != nil || pNotFoundNum != nil {
		t.Fatalf("expected nil for nonexistent numeric playlist, got %v, err=%v", pNotFoundNum, err)
	}

	pNotFound, err := FindPlaylist(ctx, database, "nonexistent")
	if err == nil || pNotFound != nil {
		t.Fatalf("expected nil for nonexistent playlist, got %v, err=%v", pNotFound, err)
	}

	// 4. MovePlaylist
	pID3, err := CreatePlaylist(ctx, database, "Deep House", 0, false)
	if err != nil {
		t.Fatalf("CreatePlaylist 3 error: %v", err)
	}
	err = MovePlaylist(ctx, database, pID3, pID1)
	if err != nil {
		t.Fatalf("MovePlaylist error: %v", err)
	}
	pMoved, _ := FindPlaylist(ctx, database, "3")
	if pMoved.ParentListID != pID1 {
		t.Errorf("expected parent ID %d, got %d", pID1, pMoved.ParentListID)
	}

	// Test circular move protection
	pParent, _ := CreatePlaylist(ctx, database, "ParentFolder", 0, true)
	pChild, _ := CreatePlaylist(ctx, database, "ChildFolder", pParent, true)
	pGrandChild, _ := CreatePlaylist(ctx, database, "GrandChildFolder", pChild, true)
	if err := MovePlaylist(ctx, database, pParent, pGrandChild); err == nil {
		t.Error("expected error moving parent inside its own descendant")
	}

	// Test MoveTrackInPlaylist error branches
	if err := MoveTrackInPlaylist(ctx, database, 9999, pParent, 1); err == nil {
		t.Error("expected error moving track from non-existent playlist")
	}
	if err := MoveTrackInPlaylist(ctx, database, pParent, 9999, 1); err == nil {
		t.Error("expected error moving track to non-existent playlist")
	}

	// 5. DeletePlaylist without force when children exist
	err = DeletePlaylist(ctx, database, pID1, false)
	if err == nil {
		t.Error("expected error deleting parent playlist without force")
	}

	// Test DeletePlaylist with single playlist (no children)
	pSingle, _ := CreatePlaylist(ctx, database, "Single", 0, false)
	if err := DeletePlaylist(ctx, database, pSingle, false); err != nil {
		t.Fatalf("DeletePlaylist single error: %v", err)
	}

	// Test DeletePlaylist with multi-level tree force delete
	pRootMulti, _ := CreatePlaylist(ctx, database, "RootMulti", 0, true)
	pMidMulti, _ := CreatePlaylist(ctx, database, "MidMulti", pRootMulti, true)
	pLeafMulti, _ := CreatePlaylist(ctx, database, "LeafMulti", pMidMulti, false)
	_ = pLeafMulti
	if err := DeletePlaylist(ctx, database, pRootMulti, true); err != nil {
		t.Fatalf("DeletePlaylist multi-level error: %v", err)
	}
	// Delete with force
	err = DeletePlaylist(ctx, database, pID1, true)
	if err != nil {
		t.Fatalf("DeletePlaylist with force error: %v", err)
	}
	pDeleted, _ := FindPlaylist(ctx, database, "1")
	if pDeleted != nil {
		t.Errorf("expected EDM to be deleted, got %v", pDeleted)
	}
	pChildDeleted, _ := FindPlaylist(ctx, database, "2")
	if pChildDeleted != nil {
		t.Errorf("expected child Tech House to be deleted, got %v", pChildDeleted)
	}
}

func TestPlaylistTrackOperations(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// Create playlists
	p1, _ := CreatePlaylist(ctx, database, "Playlist 1", 0, false)
	p2, _ := CreatePlaylist(ctx, database, "Playlist 2", 0, false)

	// Create tracks
	tr1 := &Track{
		Title:    sql.NullString{String: "Song One", Valid: true},
		Artist:   sql.NullString{String: "Artist A", Valid: true},
		Path:     sql.NullString{String: "Music/Song1.mp3", Valid: true},
		Filename: sql.NullString{String: "Song1.mp3", Valid: true},
	}
	tID1, err := InsertTrack(ctx, database, tr1)
	if err != nil || tID1 <= 0 {
		t.Fatalf("InsertTrack error: %v, id=%d", err, tID1)
	}

	tr2 := &Track{
		Title:    sql.NullString{String: "Song Two", Valid: true},
		Artist:   sql.NullString{String: "Artist B", Valid: true},
		Path:     sql.NullString{String: "Music/Song2.mp3", Valid: true},
		Filename: sql.NullString{String: "Song2.mp3", Valid: true},
	}
	tID2, err := InsertTrack(ctx, database, tr2)
	if err != nil || tID2 <= 0 {
		t.Fatalf("InsertTrack 2 error: %v, id=%d", err, tID2)
	}

	// 1. AddTrackToPlaylist
	ent1, err := AddTrackToPlaylist(ctx, database, p1, tID1)
	if err != nil || ent1 <= 0 {
		t.Fatalf("AddTrackToPlaylist error: %v", err)
	}
	ent2, err := AddTrackToPlaylist(ctx, database, p1, tID2)
	if err != nil || ent2 <= 0 {
		t.Fatalf("AddTrackToPlaylist 2 error: %v", err)
	}

	// Duplicate add returns same entity
	ent1Dup, err := AddTrackToPlaylist(ctx, database, p1, tID1)
	if err != nil || ent1Dup != ent1 {
		t.Fatalf("expected duplicate add to return existing entity ID, got %d (want %d)", ent1Dup, ent1)
	}

	tracksInP1, _ := GetPlaylistTracks(ctx, database, p1)
	if len(tracksInP1) != 2 {
		t.Fatalf("expected 2 tracks in P1, got %d", len(tracksInP1))
	}

	// 2. MoveTrackInPlaylist
	err = MoveTrackInPlaylist(ctx, database, p1, p2, tID1)
	if err != nil {
		t.Fatalf("MoveTrackInPlaylist error: %v", err)
	}

	tracksInP1AfterMove, _ := GetPlaylistTracks(ctx, database, p1)
	if len(tracksInP1AfterMove) != 1 || tracksInP1AfterMove[0].ID != tID2 {
		t.Errorf("expected 1 track in P1 after move, got %v", tracksInP1AfterMove)
	}

	tracksInP2, _ := GetPlaylistTracks(ctx, database, p2)
	if len(tracksInP2) != 1 || tracksInP2[0].ID != tID1 {
		t.Errorf("expected 1 track in P2 after move, got %v", tracksInP2)
	}

	// Move same source and dest is a no-op
	if err := MoveTrackInPlaylist(ctx, database, p2, p2, tID1); err != nil {
		t.Errorf("expected no-op move in same playlist to succeed: %v", err)
	}

	// 3. RemoveTrackFromPlaylist (middle item relink test)
	tr3 := &Track{
		Title:    sql.NullString{String: "Song Three", Valid: true},
		Artist:   sql.NullString{String: "Artist C", Valid: true},
		Path:     sql.NullString{String: "Music/Song3.mp3", Valid: true},
		Filename: sql.NullString{String: "Song3.mp3", Valid: true},
	}
	tID3, _ := InsertTrack(ctx, database, tr3)
	_, _ = AddTrackToPlaylist(ctx, database, p1, tID3) // p1 has tID2 and tID3

	// Add tID1 to p1 again
	_, _ = AddTrackToPlaylist(ctx, database, p1, tID1) // p1 has tID2, tID3, tID1
	// Remove middle track tID3
	err = RemoveTrackFromPlaylist(ctx, database, p1, tID3)
	if err != nil {
		t.Fatalf("Remove middle track error: %v", err)
	}

	err = RemoveTrackFromPlaylist(ctx, database, p2, tID1)
	if err != nil {
		t.Fatalf("RemoveTrackFromPlaylist error: %v", err)
	}
	tracksInP2AfterRm, _ := GetPlaylistTracks(ctx, database, p2)
	if len(tracksInP2AfterRm) != 0 {
		t.Errorf("expected 0 tracks in P2 after rm, got %d", len(tracksInP2AfterRm))
	}
}

func TestTrackCRUD_FindDeleteRelocate(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// Insert tracks
	tr1 := &Track{
		Title:    sql.NullString{String: "Sunrise", Valid: true},
		Artist:   sql.NullString{String: "DJ Alpha", Valid: true},
		Path:     sql.NullString{String: "/Volumes/USB1/Music/Sunrise.mp3", Valid: true},
		Filename: sql.NullString{String: "Sunrise.mp3", Valid: true},
	}
	tID1, _ := InsertTrack(ctx, database, tr1)

	// 1. FindTrack by ID and Path
	tByID, err := FindTrack(ctx, database, "1")
	if err != nil || tByID == nil || tByID.ID != tID1 {
		t.Fatalf("FindTrack by ID error: %v, got %v", err, tByID)
	}

	tByPath, err := FindTrack(ctx, database, tr1.Path.String)
	if err != nil || tByPath == nil || tByPath.ID != tID1 {
		t.Fatalf("FindTrack by filename error: %v, got %v", err, tByPath)
	}

	tByPrefix, err := FindTrack(ctx, database, "/Volumes/USB1/Music")
	if err != nil || tByPrefix != nil {
		t.Fatalf("FindTrack by prefix error: %v, got %v", err, tByPrefix)
	}

	tNotFoundNum, err := FindTrack(ctx, database, "99999")
	if err != nil || tNotFoundNum != nil {
		t.Fatalf("expected nil for nonexistent numeric track, got %v, err=%v", tNotFoundNum, err)
	}

	tNotFound, err := FindTrack(ctx, database, "nonexistent.mp3")
	if err != nil || tNotFound != nil {
		t.Fatalf("expected nil for nonexistent track, got %v, err=%v", tNotFound, err)
	}

	// 2. RelocateTracks
	relocated, err := RelocateTracks(ctx, database, "/Volumes/USB1", "/Volumes/USB2")
	if err != nil {
		t.Fatalf("RelocateTracks error: %v", err)
	}
	if relocated != 1 {
		t.Fatalf("expected 1 track relocated, got %d", relocated)
	}
	tAfterRelocate, _ := GetTrackByID(ctx, database, tID1)
	if tAfterRelocate.PathString() != "/Volumes/USB2/Music/Sunrise.mp3" {
		t.Errorf("unexpected path after relocate: %q", tAfterRelocate.PathString())
	}

	// 3. DeleteTrack
	pID, _ := CreatePlaylist(ctx, database, "Test PL", 0, false)
	_, _ = AddTrackToPlaylist(ctx, database, pID, tID1)
	err = DeleteTrack(ctx, database, tID1)
	if err != nil {
		t.Fatalf("DeleteTrack error: %v", err)
	}
	tDeleted, _ := GetTrackByID(ctx, database, tID1)
	if tDeleted != nil {
		t.Errorf("expected track to be deleted, got %v", tDeleted)
	}
	plTracks, _ := GetPlaylistTracks(ctx, database, pID)
	if len(plTracks) != 0 {
		t.Errorf("expected 0 playlist entities after track delete, got %d", len(plTracks))
	}
}

func TestPlaylistAndTrack_ValidationErrors(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()

	// Nil database checks
	if _, err := FindPlaylist(ctx, nil, "1"); err == nil {
		t.Error("expected error for nil database in FindPlaylist")
	}
	if _, err := CreatePlaylist(ctx, nil, "1", 0, false); err == nil {
		t.Error("expected error for nil database in CreatePlaylist")
	}
	if err := DeletePlaylist(ctx, nil, 1, false); err == nil {
		t.Error("expected error for nil database in DeletePlaylist")
	}
	if err := MovePlaylist(ctx, nil, 1, 0); err == nil {
		t.Error("expected error for nil database in MovePlaylist")
	}
	if _, err := AddTrackToPlaylist(ctx, nil, 1, 1); err == nil {
		t.Error("expected error for nil database in AddTrackToPlaylist")
	}
	if err := RemoveTrackFromPlaylist(ctx, nil, 1, 1); err == nil {
		t.Error("expected error for nil database in RemoveTrackFromPlaylist")
	}
	if _, err := InsertTrack(ctx, nil, &Track{}); err == nil {
		t.Error("expected error for nil database in InsertTrack")
	}
	if err := DeleteTrack(ctx, nil, 1); err == nil {
		t.Error("expected error for nil database in DeleteTrack")
	}
	if _, err := FindTrack(ctx, nil, "1"); err == nil {
		t.Error("expected error for nil database in FindTrack")
	}
	if _, err := RelocateTracks(ctx, nil, "a", "b"); err == nil {
		t.Error("expected error for nil database in RelocateTracks")
	}

	// Empty identifiers
	if _, err := FindPlaylist(ctx, database, ""); err == nil {
		t.Error("expected error for empty identifier in FindPlaylist")
	}
	if _, err := CreatePlaylist(ctx, database, "", 0, false); err == nil {
		t.Error("expected error for empty title in CreatePlaylist")
	}
	if _, err := CreatePlaylist(ctx, database, "A", 9999, false); err == nil {
		t.Error("expected error for non-existent parent in CreatePlaylist")
	}
	if err := DeletePlaylist(ctx, database, 9999, false); err == nil {
		t.Error("expected error for non-existent playlist in DeletePlaylist")
	}
	if err := MovePlaylist(ctx, database, 1, 1); err == nil {
		t.Error("expected error moving playlist to itself")
	}
	if err := MovePlaylist(ctx, database, 9999, 0); err == nil {
		t.Error("expected error for non-existent playlist in MovePlaylist")
	}
	pValid, _ := CreatePlaylist(ctx, database, "ValidPL", 0, false)
	if err := MovePlaylist(ctx, database, pValid, 9999); err == nil {
		t.Error("expected error for non-existent destination parent in MovePlaylist")
	}
	if _, err := FindTrack(ctx, database, ""); err == nil {
		t.Error("expected error for empty identifier in FindTrack")
	}
	if _, err := RelocateTracks(ctx, database, "", "new"); err == nil {
		t.Error("expected error for empty old prefix in RelocateTracks")
	}
	if err := DeleteTrack(ctx, database, 9999); err == nil {
		t.Error("expected error for non-existent track in DeleteTrack")
	}
	if _, err := AddTrackToPlaylist(ctx, database, 9999, 1); err == nil {
		t.Error("expected error for non-existent playlist in AddTrackToPlaylist")
	}
	pID, _ := CreatePlaylist(ctx, database, "P", 0, false)
	if _, err := AddTrackToPlaylist(ctx, database, pID, 9999); err == nil {
		t.Error("expected error for non-existent track in AddTrackToPlaylist")
	}
	if err := RemoveTrackFromPlaylist(ctx, database, pID, 9999); err == nil {
		t.Error("expected error for non-existent track in RemoveTrackFromPlaylist")
	}
}
