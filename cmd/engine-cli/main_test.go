package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("AGENT", "0")
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
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)
	backupOutDir := filepath.Join(t.TempDir(), "Backups")

	// 1. Backup list empty
	_, err := executeCommand("-l", libDir, "backup", "-o", backupOutDir, "--list")
	if err == nil {
		t.Log("list empty backups ok")
	}

	// 2. Backup create
	out, err := executeCommand("-l", libDir, "backup", "-o", backupOutDir)
	if err != nil {
		t.Fatalf("backup command failed: %v", err)
	}
	if strings.Contains(out, "===") {
		t.Errorf("unexpected header in backup output: %s", out)
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

	// 5. Restore with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "restore", backupSnapshot)
	if err != nil {
		t.Fatalf("restore --no-backup failed: %v", err)
	}

	// 6. Restore failure with bad backup path
	_, err = executeCommand("-l", libDir, "restore", "/nonexistent/backup")
	if err == nil {
		t.Error("expected error for nonexistent restore path")
	}
}

func TestVerifyCommand(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Verify plain output (healthy)
	out, err := executeCommand("-l", libDir, "verify")
	if err != nil {
		t.Fatalf("verify command failed: %v", err)
	}
	if strings.Contains(out, "===") {
		t.Errorf("verify output should not contain static headers '===', got: %s", out)
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
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Sync success (with unindexed file)
	_, err := executeCommand("-l", libDir, "sync")
	if err != nil {
		t.Fatalf("sync command failed: %v", err)
	}

	// 2. Sync with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "sync")
	if err != nil {
		t.Fatalf("sync --no-backup failed: %v", err)
	}

	// 3. Sync error on bad db path
	_, err = executeCommand("-l", "/nonexistent/lib", "sync")
	if err == nil {
		t.Error("expected error for nonexistent lib in sync")
	}
}

func TestArtworkCommand(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Normalize
	_, err := executeCommand("-l", libDir, "artwork", "normalize")
	if err != nil {
		t.Fatalf("artwork normalize failed: %v", err)
	}

	// 2. Normalize with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "artwork", "normalize")
	if err != nil {
		t.Fatalf("artwork normalize --no-backup failed: %v", err)
	}

	// 3. Prune
	_, err = executeCommand("-l", libDir, "artwork", "prune")
	if err != nil {
		t.Fatalf("artwork prune failed: %v", err)
	}

	// 4. Prune with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "artwork", "prune")
	if err != nil {
		t.Fatalf("artwork prune --no-backup failed: %v", err)
	}

	// 5. Errors on bad lib path
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
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. List with nested tree
	out, err := executeCommand("-l", libDir, "playlist", "list")
	if err != nil {
		t.Fatalf("playlist list failed: %v", err)
	}
	if strings.Contains(out, "===") || strings.Contains(out, "📁") || strings.Contains(out, "🎵") {
		t.Errorf("playlist list output should not contain headers or emojis, got: %s", out)
	}
	if !strings.Contains(out, "Collection") || !strings.Contains(out, "EDM Folder [id:1 tracks:0]") {
		t.Errorf("expected root Collection and playlist line, got: %s", out)
	}
	if !strings.Contains(out, "└── House Child [id:2 tracks:1]") {
		t.Errorf("expected ASCII branch line, got: %s", out)
	}

	// 2. Inspect child playlist
	inspectOut, err := executeCommand("-l", libDir, "playlist", "inspect", "2")
	if err != nil {
		t.Fatalf("playlist inspect failed: %v", err)
	}
	if strings.Contains(inspectOut, "===") || strings.Contains(inspectOut, "Tracks:") {
		t.Errorf("inspect output should not contain headers, got: %s", inspectOut)
	}
	if !strings.Contains(inspectOut, "Sample Artist - Sample Title [id:1 5:00]") {
		t.Errorf("expected track line 'Sample Artist - Sample Title [id:1 5:00]', got: %s", inspectOut)
	}

	// 2b. Inspect empty playlist (EDM Folder ID 1)
	emptyInspectOut, err := executeCommand("-l", libDir, "playlist", "inspect", "1")
	if err != nil {
		t.Fatalf("playlist inspect 1 failed: %v", err)
	}
	if !strings.Contains(emptyInspectOut, "No tracks in playlist.") {
		t.Errorf("expected 'No tracks in playlist.', got: %s", emptyInspectOut)
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

	emptyOut, err := executeCommand("-l", emptyDir, "playlist", "list")
	if err != nil {
		t.Fatalf("empty playlist list failed: %v", err)
	}
	if !strings.Contains(emptyOut, "No playlists found in database.") {
		t.Errorf("expected empty message, got: %s", emptyOut)
	}
}

func TestAgentMode_AllCommands(t *testing.T) {
	t.Setenv("AGENT", "1")
	libDir := createTestLibrary(t)
	backupOutDir := filepath.Join(t.TempDir(), "Backups")

	// 1. Verify in AGENT mode
	vOut, err := executeCommand("-l", libDir, "verify")
	if err != nil {
		t.Fatalf("verify in agent mode failed: %v", err)
	}
	if !strings.Contains(vOut, "total_tracks: 1") || !strings.Contains(vOut, "status: ") {
		t.Errorf("unexpected agent verify output: %s", vOut)
	}

	// 2. Backup in AGENT mode
	bOut, err := executeCommand("-l", libDir, "backup", "-o", backupOutDir)
	if err != nil {
		t.Fatalf("backup in agent mode failed: %v", err)
	}
	if !strings.Contains(bOut, "status: success") || !strings.Contains(bOut, "db_files: 2") {
		t.Errorf("unexpected agent backup output: %s", bOut)
	}

	// 3. Backup list in AGENT mode
	blOut, err := executeCommand("-l", libDir, "backup", "-o", backupOutDir, "--list")
	if err != nil {
		t.Fatalf("backup list in agent mode failed: %v", err)
	}
	if !strings.Contains(blOut, "count: 1") || !strings.Contains(blOut, "backup: ") {
		t.Errorf("unexpected agent backup list output: %s", blOut)
	}

	// 4. Restore in AGENT mode
	entries, _ := os.ReadDir(backupOutDir)
	snapshot := filepath.Join(backupOutDir, entries[0].Name())
	rOut, err := executeCommand("-l", libDir, "restore", snapshot)
	if err != nil {
		t.Fatalf("restore in agent mode failed: %v", err)
	}
	if !strings.Contains(rOut, "status: success") || !strings.Contains(rOut, "restored_from: ") {
		t.Errorf("unexpected agent restore output: %s", rOut)
	}

	// 5. Artwork normalize in AGENT mode
	anOut, err := executeCommand("-l", libDir, "artwork", "normalize")
	if err != nil {
		t.Fatalf("artwork normalize in agent mode failed: %v", err)
	}
	if !strings.Contains(anOut, "status: complete") || !strings.Contains(anOut, "total_tracks: 1") {
		t.Errorf("unexpected agent artwork normalize output: %s", anOut)
	}

	// 6. Artwork prune in AGENT mode
	apOut, err := executeCommand("-l", libDir, "artwork", "prune")
	if err != nil {
		t.Fatalf("artwork prune in agent mode failed: %v", err)
	}
	if !strings.Contains(apOut, "status: complete") || !strings.Contains(apOut, "deleted_db_rows: ") {
		t.Errorf("unexpected agent artwork prune output: %s", apOut)
	}

	// 7. Sync in AGENT mode
	sOut, err := executeCommand("-l", libDir, "sync")
	if err != nil {
		t.Fatalf("sync in agent mode failed: %v", err)
	}
	if !strings.Contains(sOut, "status: complete") || !strings.Contains(sOut, "unindexed_files: 1") {
		t.Errorf("unexpected agent sync output: %s", sOut)
	}

	// 8. Playlist inspect in AGENT mode
	piOut, err := executeCommand("-l", libDir, "playlist", "inspect", "2")
	if err != nil {
		t.Fatalf("playlist inspect in agent mode failed: %v", err)
	}
	if !strings.Contains(piOut, "- Sample Artist - Sample Title [id:1 5:00]") {
		t.Errorf("unexpected agent playlist inspect output: %s", piOut)
	}

	// 8b. Playlist inspect empty in AGENT mode
	piEmptyOut, err := executeCommand("-l", libDir, "playlist", "inspect", "1")
	if err != nil {
		t.Fatalf("playlist inspect 1 in agent mode failed: %v", err)
	}
	if !strings.Contains(piEmptyOut, "status: empty") {
		t.Errorf("unexpected agent empty playlist inspect output: %s", piEmptyOut)
	}

	// 9. Playlist list empty in AGENT mode
	emptyDir := t.TempDir()
	emptyDBPath := filepath.Join(emptyDir, "Database2", "m.db")
	_ = os.MkdirAll(filepath.Dir(emptyDBPath), 0755)
	emptyDB, _ := db.Open(emptyDBPath)
	_ = db.InitSchema(context.Background(), emptyDB)
	_ = emptyDB.Close()

	peOut, err := executeCommand("-l", emptyDir, "playlist", "list")
	if err != nil {
		t.Fatalf("empty playlist list in agent mode failed: %v", err)
	}
	if !strings.Contains(peOut, "status: empty") {
		t.Errorf("unexpected empty playlist output in agent mode: %s", peOut)
	}

	// 10. Backup list empty in AGENT mode
	emptyBackupDir := t.TempDir()
	beOut, err := executeCommand("-l", libDir, "backup", "-o", emptyBackupDir, "--list")
	if err != nil {
		t.Fatalf("empty backup list in agent mode failed: %v", err)
	}
	if !strings.Contains(beOut, "count: 0") {
		t.Errorf("unexpected empty backup list output in agent mode: %s", beOut)
	}

	// 11. Playlist list in AGENT mode (bulleted list)
	plAgentOut, err := executeCommand("-l", libDir, "playlist", "list")
	if err != nil {
		t.Fatalf("playlist list in agent mode failed: %v", err)
	}
	if !strings.Contains(plAgentOut, "- Collection") || !strings.Contains(plAgentOut, "  - EDM Folder [id:1 tracks:0]") || !strings.Contains(plAgentOut, "    - House Child [id:2 tracks:1]") {
		t.Errorf("unexpected agent bulleted playlist list output:\n%s", plAgentOut)
	}
}

func TestUnknownCommandsAndArgs(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	tests := []struct {
		name string
		args []string
	}{
		{"playlist unknown subcommand artwork", []string{"-l", libDir, "playlist", "artwork"}},
		{"playlist unknown subcommand foo", []string{"-l", libDir, "playlist", "foo"}},
		{"artwork unknown subcommand bar", []string{"-l", libDir, "artwork", "bar"}},
		{"root unknown command", []string{"-l", libDir, "unknown-cmd"}},
		{"verify extra arg", []string{"-l", libDir, "verify", "extra"}},
		{"sync extra arg", []string{"-l", libDir, "sync", "extra"}},
		{"playlist list extra arg", []string{"-l", libDir, "playlist", "list", "extra"}},
		{"artwork normalize extra arg", []string{"-l", libDir, "artwork", "normalize", "extra"}},
		{"artwork prune extra arg", []string{"-l", libDir, "artwork", "prune", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeCommand(tt.args...)
			if err == nil {
				t.Fatalf("expected error when running unknown command/args %v, got nil", tt.args)
			}
		})
	}
}

func TestHelpScreens_DescriptionsAndArguments(t *testing.T) {
	t.Setenv("AGENT", "0")

	tests := []struct {
		name       string
		args       []string
		wantSubstr []string
	}{
		{
			name: "playlist inspect help",
			args: []string{"playlist", "inspect", "--help"},
			wantSubstr: []string{
				"<playlist-id>",
				"Arguments:",
				"playlist list",
			},
		},
		{
			name: "restore help",
			args: []string{"restore", "--help"},
			wantSubstr: []string{
				"<backup-dir>",
				"Arguments:",
				"backup --list",
			},
		},
		{
			name: "artwork normalize help",
			args: []string{"artwork", "normalize", "--help"},
			wantSubstr: []string{
				"1:1 square",
				"--no-backup",
			},
		},
		{
			name: "artwork prune help",
			args: []string{"artwork", "prune", "--help"},
			wantSubstr: []string{
				"delete old, unneeded cover art",
				"--no-backup",
			},
		},
		{
			name: "sync help",
			args: []string{"sync", "--help"},
			wantSubstr: []string{
				"Scan your audio files",
				"--no-backup",
			},
		},
		{
			name: "verify help",
			args: []string{"verify", "--help"},
			wantSubstr: []string{
				"health check",
				"--json",
			},
		},
		{
			name: "backup help",
			args: []string{"backup", "--help"},
			wantSubstr: []string{
				"backup snapshot",
				"--list",
			},
		},
		{
			name: "playlist list help",
			args: []string{"playlist", "list", "--help"},
			wantSubstr: []string{
				"Collection",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := executeCommand(tt.args...)
			if err != nil {
				t.Fatalf("help command failed for %v: %v", tt.args, err)
			}
			for _, sub := range tt.wantSubstr {
				if !strings.Contains(out, sub) {
					t.Errorf("help output for %v missing substring %q. Output:\n%s", tt.args, sub, out)
				}
			}
		})
	}
}

func TestAgentMode_HelpScreens(t *testing.T) {
	t.Setenv("AGENT", "1")

	tests := []struct {
		name       string
		args       []string
		wantSubstr []string
	}{
		{
			name: "root help in agent mode",
			args: []string{"--help"},
			wantSubstr: []string{
				"command: engine-cli",
				"summary:",
				"Database2/m.db",
				"subcommands:",
			},
		},
		{
			name: "artwork normalize in agent mode",
			args: []string{"artwork", "normalize", "--help"},
			wantSubstr: []string{
				"command: engine-cli artwork normalize",
				"AlbumArt",
				"mutates_db: true",
				"auto_backup: true",
			},
		},
		{
			name: "verify in agent mode",
			args: []string{"verify", "--help"},
			wantSubstr: []string{
				"command: engine-cli verify",
				"Database2/m.db",
				"Track, Playlist",
			},
		},
		{
			name: "playlist inspect in agent mode",
			args: []string{"playlist", "inspect", "--help"},
			wantSubstr: []string{
				"command: engine-cli playlist inspect",
				"args: <playlist-id>",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := executeCommand(tt.args...)
			if err != nil {
				t.Fatalf("agent help failed for %v: %v", tt.args, err)
			}
			for _, sub := range tt.wantSubstr {
				if !strings.Contains(out, sub) {
					t.Errorf("agent help output for %v missing substring %q. Output:\n%s", tt.args, sub, out)
				}
			}
		})
	}
}
