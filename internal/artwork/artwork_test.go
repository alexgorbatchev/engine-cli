package artwork

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestNormalizeArtwork_SuccessAndRunner(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// Create dummy audio file on disk
	audioDir := filepath.Join(tempDir, "Music")
	_ = os.MkdirAll(audioDir, 0755)
	audioFile := filepath.Join(audioDir, "track1.m4a")
	_ = os.WriteFile(audioFile, []byte("audio dummy"), 0644)

	// Insert track in DB
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (1, 'Music/track1.m4a', 'track1.m4a');")

	artCacheDir := filepath.Join(tempDir, "Artwork")

	// Mock runner simulating ffprobe (1280x720 16:9 non-square image) and ffmpeg image operations
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"width":1280,"height":720,"codec_name":"png"}]}`), nil
		}
		if name == "ffmpeg" {
			// Find output target path in args (last arg)
			outTarget := args[len(args)-1]
			_ = os.WriteFile(outTarget, []byte("square 1400x1400 jpg bytes"), 0644)
			return []byte("ok"), nil
		}
		return nil, nil
	}

	report, err := NormalizeArtwork(ctx, database, tempDir, artCacheDir, runner)
	if err != nil {
		t.Fatalf("NormalizeArtwork failed: %v", err)
	}

	if report.NormalizedCount != 1 {
		t.Errorf("expected 1 normalized track, got %d", report.NormalizedCount)
	}

	// Verify AlbumArt row inserted and Track updated
	allArt, _ := db.GetAllAlbumArt(ctx, database)
	if len(allArt) != 1 {
		t.Fatalf("expected 1 AlbumArt row, got %d", len(allArt))
	}

	tr, _ := db.GetTrackByID(ctx, database, 1)
	if !tr.AlbumArtID.Valid || tr.AlbumArtID.Int64 != allArt[0].ID {
		t.Errorf("Track.albumArtId = %v, want %d", tr.AlbumArtID, allArt[0].ID)
	}
	if !strings.Contains(tr.AlbumArt.String, "image://planck/") {
		t.Errorf("Track.albumArt = %q", tr.AlbumArt.String)
	}
}

func TestPruneArtwork(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// Insert referenced AlbumArt and Track
	artID1, _ := db.InsertOrUpdateAlbumArt(ctx, database, "hash_referenced", []byte("art 1"))
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename, albumArtId, albumArtSourceHash) VALUES (1, 'p1', 'f1', ?, 'hash_referenced');", artID1)

	// Insert orphaned AlbumArt
	_, _ = db.InsertOrUpdateAlbumArt(ctx, database, "hash_orphaned", []byte("art 2"))

	// Create disk cache with referenced and unreferenced files
	artCacheDir := filepath.Join(tempDir, "Artwork")
	_ = os.MkdirAll(artCacheDir, 0755)
	_ = os.WriteFile(filepath.Join(artCacheDir, "hash_referenced.jpg"), []byte("ref"), 0644)
	_ = os.WriteFile(filepath.Join(artCacheDir, "hash_stale.jpg"), []byte("stale"), 0644)

	report, err := PruneArtwork(ctx, database, artCacheDir)
	if err != nil {
		t.Fatalf("PruneArtwork failed: %v", err)
	}

	if report.DeletedDBRows != 1 {
		t.Errorf("expected 1 deleted DB row, got %d", report.DeletedDBRows)
	}
	if report.DeletedDiskFiles != 1 {
		t.Errorf("expected 1 deleted disk file, got %d", report.DeletedDiskFiles)
	}

	if _, errStat := os.Stat(filepath.Join(artCacheDir, "hash_stale.jpg")); !os.IsNotExist(errStat) {
		t.Error("expected hash_stale.jpg to be removed from disk cache")
	}
	if _, errStat := os.Stat(filepath.Join(artCacheDir, "hash_referenced.jpg")); os.IsNotExist(errStat) {
		t.Error("expected hash_referenced.jpg to remain on disk cache")
	}
}

func TestArtwork_ErrorsAndEdgeCases(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Nil database errors
	if _, err := NormalizeArtwork(ctx, nil, tempDir, tempDir); err == nil {
		t.Error("expected error for nil database in NormalizeArtwork")
	}
	if _, err := PruneArtwork(ctx, nil, tempDir); err == nil {
		t.Error("expected error for nil database in PruneArtwork")
	}

	// 2. Empty musicBaseDir
	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	defer database.Close()
	_ = db.InitSchema(ctx, database)

	if _, err := NormalizeArtwork(ctx, database, "", tempDir); err == nil {
		t.Error("expected error for empty musicBaseDir")
	}

	// 3. Normalize with square artwork (already square)
	audioFile := filepath.Join(tempDir, "square.m4a")
	_ = os.WriteFile(audioFile, []byte("audio square"), 0644)
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (10, 'square.m4a', 'square.m4a');")

	// Missing on disk
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (20, 'missing.m4a', 'missing.m4a');")
	// Empty path
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (30, '', '');")

	squareRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"width":1400,"height":1400,"codec_name":"mjpeg"}]}`), nil
		}
		if name == "ffmpeg" {
			outTarget := args[len(args)-1]
			_ = os.WriteFile(outTarget, []byte("square bytes"), 0644)
			return []byte("ok"), nil
		}
		return nil, nil
	}

	report, err := NormalizeArtwork(ctx, database, tempDir, "", squareRunner)
	if err != nil {
		t.Fatalf("NormalizeArtwork square test failed: %v", err)
	}
	if report.AlreadySquare != 1 {
		t.Errorf("expected 1 alreadySquare track, got %d", report.AlreadySquare)
	}
	if report.ErrorCount != 1 {
		t.Errorf("expected 1 error track, got %d", report.ErrorCount)
	}
	if report.NoArtCount != 1 {
		t.Errorf("expected 1 noArt track, got %d", report.NoArtCount)
	}

	// 4. Probe returning invalid / zero dimensions
	noStreamRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[]}`), nil
		}
		return nil, nil
	}
	rep2, _ := NormalizeArtwork(ctx, database, tempDir, "", noStreamRunner)
	if rep2.NoArtCount != 2 {
		t.Errorf("expected 2 noArtCount, got %d", rep2.NoArtCount)
	}

	// 5. Probe returning zero width/height
	zeroDimRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"width":0,"height":0}]}`), nil
		}
		return nil, nil
	}
	rep3, _ := NormalizeArtwork(ctx, database, tempDir, "", zeroDimRunner)
	if rep3.NoArtCount != 2 {
		t.Errorf("expected 2 noArtCount, got %d", rep3.NoArtCount)
	}

	// 6. Extraction error and crop error branches
	failExtractRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"width":1280,"height":720}]}`), nil
		}
		if name == "ffmpeg" {
			return nil, os.ErrPermission
		}
		return nil, nil
	}
	rep4, _ := NormalizeArtwork(ctx, database, tempDir, "", failExtractRunner)
	if rep4.ErrorCount != 2 {
		t.Errorf("expected 2 errorCount on failed extract, got %d", rep4.ErrorCount)
	}

	// 7. Crop error branch
	failCropRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"width":1280,"height":720}]}`), nil
		}
		if name == "ffmpeg" {
			// If it's the first ffmpeg call (extract), succeed; if it's the second (crop), fail
			for _, arg := range args {
				if arg == "-vf" {
					return nil, os.ErrInvalid
				}
			}
			outTarget := args[len(args)-1]
			_ = os.WriteFile(outTarget, []byte("raw image bytes"), 0644)
			return []byte("ok"), nil
		}
		return nil, nil
	}
	rep5, _ := NormalizeArtwork(ctx, database, tempDir, "", failCropRunner)
	if rep5.ErrorCount != 2 {
		t.Errorf("expected 2 errorCount on failed crop, got %d", rep5.ErrorCount)
	}

	// 8. Closed DB error
	closedDB, _ := db.Open(filepath.Join(tempDir, "closed.db"))
	_ = closedDB.Close()
	if _, err := NormalizeArtwork(ctx, closedDB, tempDir, ""); err == nil {
		t.Error("expected error for closed database in NormalizeArtwork")
	}
	if _, err := PruneArtwork(ctx, closedDB, ""); err == nil {
		t.Error("expected error for closed database in PruneArtwork")
	}
}

func TestDefaultRunner(t *testing.T) {
	ctx := context.Background()
	out, err := defaultRunner(ctx, "echo", "test-artwork")
	if err != nil || !strings.Contains(string(out), "test-artwork") {
		t.Fatalf("defaultRunner failed: %v", err)
	}

	_, err = defaultRunner(ctx, "sh", "-c", "echo 'fail' >&2; exit 1")
	if err == nil {
		t.Error("expected error for non-zero exit with stderr")
	}
}
