package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func TestSyncLibrary_Success(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	_ = db.InitSchema(ctx, database)

	// Create audio file on disk with 20 bytes
	audioFile := filepath.Join(tempDir, "Music", "track1.m4a")
	_ = os.MkdirAll(filepath.Dir(audioFile), 0755)
	_ = os.WriteFile(audioFile, []byte("audio file 20 bytes!"), 0644)

	// Create an unindexed audio file on disk
	unindexedFile := filepath.Join(tempDir, "Music", "unindexed.mp3")
	_ = os.WriteFile(unindexedFile, []byte("unindexed mp3 file"), 0644)

	// Insert track in DB with mismatched title and fileBytes = 5
	_, _ = database.ExecContext(ctx, `
		INSERT INTO Track (id, path, filename, title, artist, fileBytes)
		VALUES (1, 'Music/track1.m4a', 'track1.m4a', 'Old Title', 'Old Artist', 5);
	`)

	// Mock runner returning new tags
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{
			"format": {
				"tags": {
					"title": "New Title",
					"artist": "New Artist",
					"album": "New Album",
					"genre": "Techno",
					"date": "2023-05-10"
				}
			}
		}`), nil
	}

	report, err := SyncLibrary(ctx, database, tempDir, runner)
	if err != nil {
		t.Fatalf("SyncLibrary failed: %v", err)
	}

	if report.UpdatedCount != 1 {
		t.Errorf("expected 1 updated track, got %d", report.UpdatedCount)
	}
	if report.UnindexedCount != 1 {
		t.Errorf("expected 1 unindexed file, got %d", report.UnindexedCount)
	}

	// Verify DB record updated
	tr, _ := db.GetTrackByID(ctx, database, 1)
	if tr.Title.String != "New Title" || tr.Artist.String != "New Artist" || tr.FileBytes.Int64 != 20 || tr.Year.Int64 != 2023 {
		t.Errorf("track not updated properly in DB: %+v", tr)
	}
}

func TestSyncLibrary_ErrorsAndEdgeCases(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Nil database
	if _, err := SyncLibrary(ctx, nil, tempDir); err == nil {
		t.Error("expected error for nil database")
	}

	// 2. Empty musicBaseDir
	dbPath := filepath.Join(tempDir, "m.db")
	database, _ := db.Open(dbPath)
	defer database.Close()
	_ = db.InitSchema(ctx, database)

	if _, err := SyncLibrary(ctx, database, ""); err == nil {
		t.Error("expected error for empty musicBaseDir")
	}

	// 3. Missing track and empty path
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (10, '', '');")
	_, _ = database.ExecContext(ctx, "INSERT INTO Track (id, path, filename) VALUES (20, 'Music/missing.m4a', 'missing.m4a');")

	rep, err := SyncLibrary(ctx, database, tempDir)
	if err != nil {
		t.Fatalf("SyncLibrary missing tracks test failed: %v", err)
	}
	if rep.MissingCount != 2 {
		t.Errorf("expected 2 missing tracks, got %d", rep.MissingCount)
	}

	// 4. Closed database error
	closedDB, _ := db.Open(filepath.Join(tempDir, "closed.db"))
	_ = closedDB.Close()
	if _, err := SyncLibrary(ctx, closedDB, tempDir); err == nil {
		t.Error("expected error for closed database")
	}
}

func TestDefaultRunner(t *testing.T) {
	ctx := context.Background()
	out, err := defaultRunner(ctx, "echo", "test-sync")
	if err != nil || string(out) == "" {
		t.Fatalf("defaultRunner failed: %v", err)
	}

	_, err = defaultRunner(ctx, "sh", "-c", "echo 'err' >&2; exit 1")
	if err == nil {
		t.Error("expected error on non-zero exit")
	}
}
