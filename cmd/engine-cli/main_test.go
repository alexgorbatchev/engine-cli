package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func createTestLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	db2Dir := filepath.Join(dir, "Database2")
	_ = os.MkdirAll(db2Dir, 0755)

	dbPath := filepath.Join(db2Dir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	_ = db.InitSchema(ctx, database)

	// Create sample audio file
	audioDir := filepath.Join(dir, "Music")
	_ = os.MkdirAll(audioDir, 0755)
	audioFile := filepath.Join(audioDir, "track1.m4a")
	_ = os.WriteFile(audioFile, []byte("audio dummy 20 bytes"), 0644)

	// Create unindexed file
	unindexedFile := filepath.Join(dir, "Music", "unindexed.mp3")
	_ = os.WriteFile(unindexedFile, []byte("unindexed audio"), 0644)

	// Insert Track
	_, _ = database.ExecContext(ctx, `
		INSERT INTO Track (id, title, artist, path, filename, fileBytes, length, bpm)
		VALUES (1, 'Sample Title', 'Sample Artist', 'Music/track1.m4a', 'track1.m4a', 20, 300, 128);
	`)

	// Insert Nested Playlist (Folder 1 -> Child 2)
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (1, 'EDM Folder', 0);")
	_, _ = database.ExecContext(ctx, "INSERT INTO Playlist (id, title, parentListId) VALUES (2, 'House Child', 1);")
	_, _ = database.ExecContext(ctx, "INSERT INTO PlaylistEntity (listId, trackId) VALUES (2, 1);")

	// Insert root m.db
	_ = os.WriteFile(filepath.Join(dir, "m.db"), []byte("sample root m.db"), 0644)

	// Create Artwork dir
	artDir := filepath.Join(dir, "Artwork")
	_ = os.MkdirAll(artDir, 0755)
	_ = os.WriteFile(filepath.Join(artDir, "test.jpg"), []byte("test art"), 0644)

	return dir
}

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd := newRootCommand()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

func TestRootCommand_HelpAndVersion(t *testing.T) {
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected help output")
	}

	outVer, errVer := executeCommand("--version")
	if errVer != nil {
		t.Fatalf("version failed: %v", errVer)
	}
	if outVer != "1.0.0\n" {
		t.Errorf("expected '1.0.0\\n', got %q", outVer)
	}

	_ = getDefaultLibraryDir()
}

func TestBackupAndRestoreCommand(t *testing.T) {
	libDir := createTestLibrary(t)
	backupOutDir := filepath.Join(t.TempDir(), "Backups")

	// 1. Backup list empty
	_, err := executeCommand("-l", libDir, "backup", "-o", backupOutDir, "--list")
	if err == nil {
		t.Log("list empty backups ok")
	}

	// 2. Backup create
	_, err = executeCommand("-l", libDir, "backup", "-o", backupOutDir)
	if err != nil {
		t.Fatalf("backup command failed: %v", err)
	}

	// 3. Backup list populated
	_, err = executeCommand("-l", libDir, "backup", "-o", backupOutDir, "--list")
	if err != nil {
		t.Fatalf("backup --list failed: %v", err)
	}

	// 4. Restore
	entries, _ := os.ReadDir(backupOutDir)
	if len(entries) == 0 {
		t.Fatal("no backup directory created")
	}
	backupSnapshot := filepath.Join(backupOutDir, entries[0].Name())

	_, err = executeCommand("-l", libDir, "restore", backupSnapshot)
	if err != nil {
		t.Fatalf("restore command failed: %v", err)
	}

	// 5. Restore failure with bad backup path
	_, err = executeCommand("-l", libDir, "restore", "/nonexistent/backup")
	if err == nil {
		t.Error("expected error for nonexistent restore path")
	}
}

func TestVerifyCommand(t *testing.T) {
	libDir := createTestLibrary(t)

	// 1. Verify plain output (healthy)
	_, err := executeCommand("-l", libDir, "verify")
	if err != nil {
		t.Fatalf("verify command failed: %v", err)
	}

	// 2. Verify JSON output
	_, err = executeCommand("-l", libDir, "verify", "--json")
	if err != nil {
		t.Fatalf("verify --json failed: %v", err)
	}

	// 3. Verify with detected issues
	dbPath := filepath.Join(libDir, "Database2", "m.db")
	database, _ := db.Open(dbPath)
	_, _ = database.ExecContext(context.Background(), "INSERT INTO Track (id, path, filename, fileBytes) VALUES (999, 'Music/missing.m4a', 'missing.m4a', 100);")
	_ = database.Close()

	_, err = executeCommand("-l", libDir, "verify")
	if err != nil {
		t.Fatalf("verify with issues failed: %v", err)
	}

	// 4. Verify on bad database path
	_, err = executeCommand("-l", "/nonexistent/lib", "verify")
	if err == nil {
		t.Error("expected error for nonexistent lib in verify")
	}
}

func TestSyncCommand(t *testing.T) {
	libDir := createTestLibrary(t)

	// 1. Sync success (with unindexed file)
	_, err := executeCommand("-l", libDir, "sync")
	if err != nil {
		t.Fatalf("sync command failed: %v", err)
	}

	// 2. Sync error on bad db path
	_, err = executeCommand("-l", "/nonexistent/lib", "sync")
	if err == nil {
		t.Error("expected error for nonexistent lib in sync")
	}
}

func TestArtworkCommand(t *testing.T) {
	libDir := createTestLibrary(t)

	// 1. Normalize
	_, err := executeCommand("-l", libDir, "artwork", "normalize")
	if err != nil {
		t.Fatalf("artwork normalize failed: %v", err)
	}

	// 2. Prune
	_, err = executeCommand("-l", libDir, "artwork", "prune")
	if err != nil {
		t.Fatalf("artwork prune failed: %v", err)
	}

	// 3. Errors on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "artwork", "normalize")
	if err == nil {
		t.Error("expected error for nonexistent lib in artwork normalize")
	}
	_, err = executeCommand("-l", "/nonexistent/lib", "artwork", "prune")
	if err == nil {
		t.Error("expected error for nonexistent lib in artwork prune")
	}
}

func TestPlaylistCommand(t *testing.T) {
	libDir := createTestLibrary(t)

	// 1. List with nested tree
	_, err := executeCommand("-l", libDir, "playlist", "list")
	if err != nil {
		t.Fatalf("playlist list failed: %v", err)
	}

	// 2. Inspect child playlist
	_, err = executeCommand("-l", libDir, "playlist", "inspect", "2")
	if err != nil {
		t.Fatalf("playlist inspect failed: %v", err)
	}

	// 3. Inspect invalid ID string
	_, err = executeCommand("-l", libDir, "playlist", "inspect", "invalid-id")
	if err == nil {
		t.Error("expected error for invalid playlist ID")
	}

	// 4. Inspect non-existent ID
	_, err = executeCommand("-l", libDir, "playlist", "inspect", "99999")
	if err == nil {
		t.Error("expected error for nonexistent playlist ID")
	}

	// 5. List error on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "playlist", "list")
	if err == nil {
		t.Error("expected error for nonexistent lib in playlist list")
	}

	// 6. Inspect error on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "playlist", "inspect", "1")
	if err == nil {
		t.Error("expected error for nonexistent lib in playlist inspect")
	}

	// 7. Empty playlist list
	emptyDir := t.TempDir()
	emptyDBPath := filepath.Join(emptyDir, "Database2", "m.db")
	_ = os.MkdirAll(filepath.Dir(emptyDBPath), 0755)
	emptyDB, _ := db.Open(emptyDBPath)
	_ = db.InitSchema(context.Background(), emptyDB)
	_ = emptyDB.Close()

	_, err = executeCommand("-l", emptyDir, "playlist", "list")
	if err != nil {
		t.Fatalf("empty playlist list failed: %v", err)
	}
}
