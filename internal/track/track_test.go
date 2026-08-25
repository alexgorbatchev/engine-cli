package track

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func createTestDB(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	libDir := t.TempDir()
	dbDir := filepath.Join(libDir, "Database2")
	_ = os.MkdirAll(dbDir, 0o755)
	dbPath := filepath.Join(dbDir, "m.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	ctx := context.Background()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	t.Cleanup(func() {
		_ = database.Close()
	})

	return database, libDir, dbPath
}

func fakeRunner(tagsJSON string) func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(tagsJSON), nil
	}
}

func TestAddTrack_Success(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	// Create a dummy audio file outside libDir
	srcDir := t.TempDir()
	srcAudio := filepath.Join(srcDir, "sample.mp3")
	if err := os.WriteFile(srcAudio, []byte("fake mp3 audio contents 12345"), 0o644); err != nil {
		t.Fatalf("writing fake audio: %v", err)
	}

	tagsJSON := `{
		"format": {
			"duration": "245.5",
			"bit_rate": "320000",
			"tags": {
				"title": "Summer Vibes",
				"artist": "DJ Sun",
				"album": "Sunshine EP",
				"genre": "Melodic House",
				"date": "2024-06-01",
				"bpm": "124.0"
			}
		}
	}`

	// 1. Add track with --move and --to-playlist
	res, err := AddTrack(ctx, database, libDir, srcAudio, "Summer Playlist", true, fakeRunner(tagsJSON))
	if err != nil {
		t.Fatalf("AddTrack error: %v", err)
	}

	if res.Track == nil || res.Track.ID <= 0 {
		t.Fatalf("expected valid track in result, got %v", res.Track)
	}
	if res.Track.Title.String != "Summer Vibes" {
		t.Errorf("expected title 'Summer Vibes', got %q", res.Track.Title.String)
	}
	if res.Track.Artist.String != "DJ Sun" {
		t.Errorf("expected artist 'DJ Sun', got %q", res.Track.Artist.String)
	}
	if res.Track.BPM.Int64 != 124 {
		t.Errorf("expected BPM 124, got %d", res.Track.BPM.Int64)
	}
	if res.Track.Year.Int64 != 2024 {
		t.Errorf("expected Year 2024, got %d", res.Track.Year.Int64)
	}
	if res.Track.Length.Int64 != 245 {
		t.Errorf("expected Length 245s, got %d", res.Track.Length.Int64)
	}
	if res.Track.Bitrate.Int64 != 320 {
		t.Errorf("expected Bitrate 320 kbps, got %d", res.Track.Bitrate.Int64)
	}
	if res.Playlist == nil || res.Playlist.Title != "Summer Playlist" {
		t.Fatalf("expected playlist 'Summer Playlist', got %v", res.Playlist)
	}
	if !res.Moved {
		t.Error("expected Moved = true")
	}

	// Verify original file moved
	if _, err := os.Stat(srcAudio); !os.IsNotExist(err) {
		t.Errorf("expected source file to be moved, but still exists")
	}

	// Verify file in libDir/Music/sample.mp3
	destAudio := filepath.Join(libDir, "Music", "sample.mp3")
	if _, err := os.Stat(destAudio); err != nil {
		t.Errorf("expected moved audio at %q, err=%v", destAudio, err)
	}

	// 2. Add same track again (updates metadata)
	res2, err := AddTrack(ctx, database, libDir, destAudio, "Summer Playlist", false, fakeRunner(tagsJSON))
	if err != nil {
		t.Fatalf("AddTrack second call error: %v", err)
	}
	if res2.Track.ID != res.Track.ID {
		t.Errorf("expected existing track ID %d, got %d", res.Track.ID, res2.Track.ID)
	}

	// 3. Add with moveFile = true when file is already in libDir/Music
	res3, err := AddTrack(ctx, database, libDir, destAudio, "", true, fakeRunner(tagsJSON))
	if err != nil {
		t.Fatalf("AddTrack already in Music error: %v", err)
	}
	if res3.Track.ID != res.Track.ID {
		t.Errorf("expected existing track ID %d, got %d", res.Track.ID, res3.Track.ID)
	}
}

func TestAddTrack_NoTagsFallback(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "track_without_tags.wav")
	_ = os.WriteFile(audioPath, []byte("wav data"), 0o644)

	res, err := AddTrack(ctx, database, libDir, audioPath, "", false, nil)
	if err != nil {
		t.Fatalf("AddTrack error: %v", err)
	}
	if res.Track.Title.String != "track_without_tags" {
		t.Errorf("expected title 'track_without_tags', got %q", res.Track.Title.String)
	}
	if res.Playlist != nil {
		t.Errorf("expected no playlist, got %v", res.Playlist)
	}
}

func TestMoveTrackFile(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "Music", "orig.mp3")
	_ = os.MkdirAll(filepath.Dir(audioPath), 0o755)
	_ = os.WriteFile(audioPath, []byte("orig data 1234"), 0o644)

	_, _ = AddTrack(ctx, database, libDir, audioPath, "", false, nil)

	// Move to new folder
	newDir := filepath.Join(libDir, "Music", "House")
	_ = os.MkdirAll(newDir, 0o755)
	movedTrack, err := MoveTrackFile(ctx, database, libDir, "1", newDir)
	if err != nil {
		t.Fatalf("MoveTrackFile error: %v", err)
	}

	if movedTrack.Filename.String != "orig.mp3" {
		t.Errorf("expected filename 'orig.mp3', got %q", movedTrack.Filename.String)
	}
	if _, err := os.Stat(filepath.Join(newDir, "orig.mp3")); err != nil {
		t.Errorf("expected file in %q, got err=%v", newDir, err)
	}
}

func TestRemoveTrack(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "Music", "delete_me.mp3")
	_ = os.MkdirAll(filepath.Dir(audioPath), 0o755)
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)

	_, _ = AddTrack(ctx, database, libDir, audioPath, "PlaylistX", false, nil)

	// Remove with deleteFile = true
	rmTrack, err := RemoveTrack(ctx, database, libDir, "delete_me.mp3", true)
	if err != nil {
		t.Fatalf("RemoveTrack error: %v", err)
	}
	if rmTrack.Filename.String != "delete_me.mp3" {
		t.Errorf("expected filename delete_me.mp3, got %q", rmTrack.Filename.String)
	}

	// Verify file deleted from disk
	if _, err := os.Stat(audioPath); !os.IsNotExist(err) {
		t.Errorf("expected audio file to be deleted from disk")
	}

	// Verify track deleted from DB
	tFound, _ := db.FindTrack(ctx, database, "delete_me.mp3")
	if tFound != nil {
		t.Errorf("expected track to be deleted from DB, got %v", tFound)
	}
}

func TestRelocateTracks(t *testing.T) {
	database, _, _ := createTestDB(t)
	ctx := context.Background()

	tr := &db.Track{
		Title:    sql.NullString{String: "Song", Valid: true},
		Path:     sql.NullString{String: "/Volumes/OldUSB/Music/song.mp3", Valid: true},
		Filename: sql.NullString{String: "song.mp3", Valid: true},
	}
	_, _ = db.InsertTrack(ctx, database, tr)

	count, err := RelocateTracks(ctx, database, "/Volumes/OldUSB", "/Volumes/NewUSB")
	if err != nil {
		t.Fatalf("RelocateTracks error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 track relocated, got %d", count)
	}

	tUpdated, _ := db.FindTrack(ctx, database, "song.mp3")
	if tUpdated.PathString() != "/Volumes/NewUSB/Music/song.mp3" {
		t.Errorf("expected relocated path, got %q", tUpdated.PathString())
	}
}

func TestTrack_ClosedDBErrors(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "closed_db_track.mp3")
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)
	_, _ = AddTrack(ctx, database, libDir, audioPath, "", false, nil)

	_ = database.Close()

	if _, err := AddTrack(ctx, database, libDir, audioPath, "", false, nil); err == nil {
		t.Error("expected error on closed db AddTrack")
	}
	if _, err := RemoveTrack(ctx, database, libDir, "1", false); err == nil {
		t.Error("expected error on closed db RemoveTrack")
	}
	if _, err := MoveTrackFile(ctx, database, libDir, "1", "new_path"); err == nil {
		t.Error("expected error on closed db MoveTrackFile")
	}
}

func TestTrack_ValidationAndErrors(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	// Nil database checks
	if _, err := AddTrack(ctx, nil, libDir, "a.mp3", "", false); err == nil {
		t.Error("expected error for nil db in AddTrack")
	}
	if _, err := RemoveTrack(ctx, nil, libDir, "1", false); err == nil {
		t.Error("expected error for nil db in RemoveTrack")
	}
	if _, err := MoveTrackFile(ctx, nil, libDir, "1", "dest"); err == nil {
		t.Error("expected error for nil db in MoveTrackFile")
	}

	// Empty paths / non-existent tracks
	if _, err := AddTrack(ctx, database, libDir, "", "", false); err == nil {
		t.Error("expected error for empty path in AddTrack")
	}
	if _, err := AddTrack(ctx, database, libDir, filepath.Join(libDir, "nonexistent.mp3"), "", false); err == nil {
		t.Error("expected error for nonexistent file in AddTrack")
	}
	if _, err := AddTrack(ctx, database, libDir, libDir, "", false); err == nil {
		t.Error("expected error for directory in AddTrack")
	}
	if _, err := RemoveTrack(ctx, database, libDir, "nonexistent", false); err == nil {
		t.Error("expected error removing non-existent track")
	}
	if _, err := MoveTrackFile(ctx, database, libDir, "1", ""); err == nil {
		t.Error("expected error for empty dest in MoveTrackFile")
	}
	if _, err := MoveTrackFile(ctx, database, libDir, "nonexistent", "dest"); err == nil {
		t.Error("expected error moving non-existent track")
	}
}

func TestMoveFileOnDisk(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "source.txt")
	dst := filepath.Join(tempDir, "dest.txt")

	_ = os.WriteFile(src, []byte("content"), 0o644)
	if err := moveFileOnDisk(src, dst); err != nil {
		t.Fatalf("moveFileOnDisk failed: %v", err)
	}

	// Error when source does not exist
	if err := moveFileOnDisk(filepath.Join(tempDir, "nonexistent.txt"), dst); err == nil {
		t.Error("expected error moving nonexistent file")
	}

	// Test copyAndRemoveFile directly
	src2 := filepath.Join(tempDir, "source2.txt")
	dst2 := filepath.Join(tempDir, "dest2.txt")
	_ = os.WriteFile(src2, []byte("content2"), 0o644)
	if err := copyAndRemoveFile(src2, dst2); err != nil {
		t.Fatalf("copyAndRemoveFile failed: %v", err)
	}
	// Test copyAndRemoveFile errors
	if err := copyAndRemoveFile(filepath.Join(tempDir, "nonexistent.txt"), dst2); err == nil {
		t.Error("expected error opening nonexistent src")
	}
	if err := copyAndRemoveFile(src2, filepath.Join(tempDir, "nonexistent_dir", "sub", "dest.txt")); err == nil {
		t.Error("expected error creating destination in nonexistent dir")
	}
}

func TestAddTrack_ExistingPlaylistAndAlternateTags(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	// Pre-create playlist
	pID, _ := db.CreatePlaylist(ctx, database, "ExistingPL", 0, false)
	_ = pID

	audioPath := filepath.Join(libDir, "tbpm_track.mp3")
	_ = os.WriteFile(audioPath, []byte("audio-bytes"), 0o644)

	tagsJSON := `{
		"format": {
			"duration": "180.0",
			"bit_rate": "256000",
			"tags": {
				"title": "TBPM Track",
				"year": "2023",
				"TBPM": "128.5"
			}
		}
	}`

	res, err := AddTrack(ctx, database, libDir, audioPath, "ExistingPL", false, fakeRunner(tagsJSON))
	if err != nil {
		t.Fatalf("AddTrack error: %v", err)
	}
	if res.Track.BPM.Int64 != 128 {
		t.Errorf("expected BPM 128, got %d", res.Track.BPM.Int64)
	}
	if res.Track.Year.Int64 != 2023 {
		t.Errorf("expected Year 2023, got %d", res.Track.Year.Int64)
	}
	if res.Playlist.Title != "ExistingPL" {
		t.Errorf("expected playlist ExistingPL, got %s", res.Playlist.Title)
	}
}

func TestMoveTrackFile_TrailingSlashAndSameDest(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "Music", "song.mp3")
	_ = os.MkdirAll(filepath.Dir(audioPath), 0o755)
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)

	_, _ = AddTrack(ctx, database, libDir, audioPath, "", false, nil)

	// Move with trailing slash
	destDir := filepath.Join(libDir, "Music", "Sub") + "/"
	moved, err := MoveTrackFile(ctx, database, libDir, "1", destDir)
	if err != nil {
		t.Fatalf("MoveTrackFile trailing slash error: %v", err)
	}
	if moved.Filename.String != "song.mp3" {
		t.Errorf("expected filename song.mp3, got %s", moved.Filename.String)
	}

	// Move to direct file path
	directFile := filepath.Join(libDir, "Music", "Sub", "renamed.mp3")
	movedDirect, err := MoveTrackFile(ctx, database, libDir, "1", directFile)
	if err != nil {
		t.Fatalf("MoveTrackFile direct file error: %v", err)
	}
	if movedDirect.Filename.String != "renamed.mp3" {
		t.Errorf("expected filename renamed.mp3, got %s", movedDirect.Filename.String)
	}
}

func TestRemoveTrack_KeepFile(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "Music", "keep_me.mp3")
	_ = os.MkdirAll(filepath.Dir(audioPath), 0o755)
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)

	_, _ = AddTrack(ctx, database, libDir, audioPath, "", false, nil)

	// Remove with deleteFile = false
	_, err := RemoveTrack(ctx, database, libDir, "keep_me.mp3", false)
	if err != nil {
		t.Fatalf("RemoveTrack error: %v", err)
	}

	// Verify file still exists on disk
	if _, err := os.Stat(audioPath); err != nil {
		t.Errorf("expected audio file to remain on disk: %v", err)
	}

	// Remove track with absolute path and null path
	absTrack := &db.Track{
		Path:     sql.NullString{String: audioPath, Valid: true},
		Filename: sql.NullString{String: "keep_me.mp3", Valid: true},
	}
	absID, _ := db.InsertTrack(ctx, database, absTrack)
	if _, err := RemoveTrack(ctx, database, libDir, strconv.FormatInt(absID, 10), true); err != nil {
		t.Fatalf("RemoveTrack absolute path error: %v", err)
	}

	nullTrack := &db.Track{
		Filename: sql.NullString{String: "null_path.mp3", Valid: true},
	}
	nullID, _ := db.InsertTrack(ctx, database, nullTrack)
	if _, err := RemoveTrack(ctx, database, libDir, strconv.FormatInt(nullID, 10), true); err != nil {
		t.Fatalf("RemoveTrack null path error: %v", err)
	}
}

func TestAddTrack_EmptyTitleTagInJSON(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "empty_title_tag.mp3")
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)

	tagsJSON := `{
		"format": {
			"duration": "100.0",
			"tags": {
				"artist": "Only Artist"
			}
		}
	}`

	res, err := AddTrack(ctx, database, libDir, audioPath, "", false, fakeRunner(tagsJSON))
	if err != nil {
		t.Fatalf("AddTrack error: %v", err)
	}
	if res.Track.Title.String != "empty_title_tag" {
		t.Errorf("expected title fallback to filename, got %s", res.Track.Title.String)
	}
}

func TestMoveTrackFile_SamePath(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	ctx := context.Background()

	audioPath := filepath.Join(libDir, "Music", "samesong.mp3")
	_ = os.MkdirAll(filepath.Dir(audioPath), 0o755)
	_ = os.WriteFile(audioPath, []byte("data"), 0o644)

	_, _ = AddTrack(ctx, database, libDir, audioPath, "", false, nil)

	moved, err := MoveTrackFile(ctx, database, libDir, "1", audioPath)
	if err != nil {
		t.Fatalf("MoveTrackFile same path error: %v", err)
	}
	if moved.Filename.String != "samesong.mp3" {
		t.Errorf("expected filename samesong.mp3, got %s", moved.Filename.String)
	}
}

func TestDefaultRunner(t *testing.T) {
	ctx := context.Background()
	out, err := defaultRunner(ctx, "echo", "test-output")
	if err != nil {
		t.Fatalf("defaultRunner echo error: %v", err)
	}
	if len(out) == 0 {
		t.Errorf("expected non-empty output from echo")
	}

	// Non-existent binary error
	_, err = defaultRunner(ctx, "nonexistent-cmd-xyz-12345")
	if err == nil {
		t.Error("expected error from non-existent command")
	}
}
