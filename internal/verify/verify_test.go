package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestVerifyLibrary_Healthy(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	// Create real audio file on disk
	audioFile := filepath.Join(tempDir, "Music", "Track1.m4a")
	_ = os.MkdirAll(filepath.Dir(audioFile), 0755)
	_ = os.WriteFile(audioFile, []byte("audio 12345"), 0644)

	// Insert AlbumArt
	artID, _ := db.InsertOrUpdateAlbumArt(ctx, database, "hash1", []byte("art blob"))

	// Insert Track matching disk size
	relPath := "Music/Track1.m4a"
	_, _ = database.ExecContext(ctx, `
		INSERT INTO Track (id, path, filename, fileBytes, albumArtId)
		VALUES (1, ?, 'Track1.m4a', 11, ?);
	`, relPath, artID)

	// Insert Playlist with track
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (1, 'Favorites', 0);")
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (1, 1);")

	report, err := VerifyLibrary(ctx, database, tempDir)
	if err != nil {
		t.Fatalf("VerifyLibrary error: %v", err)
	}

	if !report.IsHealthy {
		t.Errorf("expected healthy library, got issues: %v", report.Issues)
	}
	if report.TotalTracks != 1 || report.TotalPlaylists != 1 {
		t.Errorf("unexpected counts: tracks=%d, playlists=%d", report.TotalTracks, report.TotalPlaylists)
	}
}

func TestVerifyLibrary_IssuesDetected(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// 1. Missing on disk track
	_, _ = database.ExecContext(ctx, `
		INSERT INTO Track (id, path, filename, fileBytes, albumArtId)
		VALUES (10, 'Music/Nonexistent.m4a', 'Nonexistent.m4a', 100, 999);
	`)

	// 2. File size mismatch track
	existingFile := filepath.Join(tempDir, "Music", "Track2.m4a")
	_ = os.MkdirAll(filepath.Dir(existingFile), 0755)
	_ = os.WriteFile(existingFile, []byte("real content is 19 bytes"), 0644)

	_, _ = database.ExecContext(ctx, `
		INSERT INTO Track (id, path, filename, fileBytes)
		VALUES (20, 'Music/Track2.m4a', 'Track2.m4a', 500);
	`)

	// 3. Orphaned playlist entity & empty playlist
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (100, 'Empty List', 0);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (200, 'Orphan List', 0);")
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (200, 9999);") // 9999 does not exist

	report, err := VerifyLibrary(ctx, database, tempDir)
	if err != nil {
		t.Fatalf("VerifyLibrary error: %v", err)
	}

	if report.IsHealthy {
		t.Error("expected unhealthy library")
	}
	if report.MissingOnDiskCount != 1 {
		t.Errorf("expected 1 missing on disk, got %d", report.MissingOnDiskCount)
	}
	if report.BytesMismatchCount != 1 {
		t.Errorf("expected 1 bytes mismatch, got %d", report.BytesMismatchCount)
	}
	if report.MissingArtCount != 1 {
		t.Errorf("expected 1 missing art, got %d", report.MissingArtCount)
	}
	if report.OrphanedCount != 1 {
		t.Errorf("expected 1 orphaned entity, got %d", report.OrphanedCount)
	}
	if report.EmptyPlaylistCount != 2 {
		t.Errorf("expected 2 empty playlists, got %d", report.EmptyPlaylistCount)
	}
}

func TestVerifyLibrary_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	if _, err := VerifyLibrary(ctx, nil, tempDir); err == nil {
		t.Error("expected error for nil database")
	}

	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	_ = database.Close()

	if _, err := VerifyLibrary(ctx, database, ""); err == nil {
		t.Error("expected error for empty musicBaseDir")
	}
	if _, err := VerifyLibrary(ctx, database, tempDir); err == nil {
		t.Error("expected error for closed database")
	}
}
