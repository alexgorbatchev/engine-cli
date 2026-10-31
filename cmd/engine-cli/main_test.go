package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func createTestLibrary(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()

	db2Dir := filepath.Join(dir, "Database2")
	if err := os.MkdirAll(db2Dir, 0755); err != nil {
		t.Fatalf("create collection database directory: %v", err)
	}

	dbPath := filepath.Join(db2Dir, "m.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open collection fixture database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close collection fixture database: %v", err)
		}
	}()

	ctx := context.Background()
	if err := db.InitSchema(ctx, database); err != nil {
		t.Fatalf("initialize collection fixture schema: %v", err)
	}

	// Create sample audio file
	audioDir := filepath.Join(dir, "Music")
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		t.Fatalf("create fixture audio directory: %v", err)
	}
	audioFile := filepath.Join(audioDir, "track1.m4a")
	if err := os.WriteFile(audioFile, []byte("audio dummy 20 bytes"), 0644); err != nil {
		t.Fatalf("write fixture audio file: %v", err)
	}

	// Create unindexed file
	unindexedFile := filepath.Join(dir, "Music", "unindexed.mp3")
	if err := os.WriteFile(unindexedFile, []byte("unindexed audio"), 0644); err != nil {
		t.Fatalf("write unindexed fixture audio file: %v", err)
	}

	for _, statement := range []struct{ name, sql string }{
		{"track", `
		INSERT INTO Track (id, title, artist, path, filename, fileBytes, length, bpm)
		VALUES (1, 'Sample Title', 'Sample Artist', 'Music/track1.m4a', 'track1.m4a', 20, 300, 128);
		`},
		{"parent playlist", "INSERT INTO Playlist (id, title, parentListId) VALUES (1, 'EDM Folder', 0);"},
		{"child playlist", "INSERT INTO Playlist (id, title, parentListId) VALUES (2, 'House Child', 1);"},
		{"playlist membership", "INSERT INTO PlaylistEntity (listId, trackId, databaseUuid) VALUES (2, 1, 'fixture');"},
	} {
		if _, err := database.ExecContext(ctx, statement.sql); err != nil {
			t.Fatalf("insert fixture %s: %v", statement.name, err)
		}
	}

	// Root database fixtures must be valid SQLite, just like Database2/m.db.
	rootDB, err := db.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatalf("open root fixture database: %v", err)
	}
	defer func() {
		if err := rootDB.Close(); err != nil {
			t.Fatalf("close root fixture database: %v", err)
		}
	}()
	if _, err := rootDB.Exec("CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES('sample root m.db')"); err != nil {
		t.Fatalf("populate root fixture database: %v", err)
	}

	// Create Artwork dir
	artDir := filepath.Join(dir, "Artwork")
	if err := os.MkdirAll(artDir, 0755); err != nil {
		t.Fatalf("create fixture artwork directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artDir, "test.jpg"), []byte("test art"), 0644); err != nil {
		t.Fatalf("write fixture artwork file: %v", err)
	}

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

func createEmptyTestDatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create empty fixture database directory: %v", err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open empty fixture database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatalf("close empty fixture database: %v", err)
		}
	}()
	if err := db.InitSchema(t.Context(), database); err != nil {
		t.Fatalf("initialize empty fixture schema: %v", err)
	}
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
	if outVer != "1.1.0\n" {
		t.Errorf("expected '1.1.0\\n', got %q", outVer)
	}

	_ = getDefaultLibraryDir()
}

func TestLibraryBackupAndRestoreCommand(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)
	backupOutDir := filepath.Join(t.TempDir(), "Backups")

	// 1. Backup list empty
	_, err := executeCommand("-l", libDir, "library", "backup", backupOutDir, "--list")
	if err == nil {
		t.Log("list empty backups ok")
	}

	// 2. Backup create with positional path
	out, err := executeCommand("-l", libDir, "library", "backup", backupOutDir)
	if err != nil {
		t.Fatalf("backup command failed: %v", err)
	}
	if strings.Contains(out, "===") {
		t.Errorf("unexpected header in backup output: %s", out)
	}

	// 3. Backup list populated
	_, err = executeCommand("-l", libDir, "library", "backup", backupOutDir, "--list")
	if err != nil {
		t.Fatalf("backup --list failed: %v", err)
	}

	// 4. Restore with positional path
	entries, err := os.ReadDir(backupOutDir)
	if err != nil {
		t.Fatalf("read fixture backup directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no backup directory created")
	}
	backupSnapshot := filepath.Join(backupOutDir, entries[0].Name())

	_, err = executeCommand("-l", libDir, "library", "restore", backupSnapshot)
	if err != nil {
		t.Fatalf("restore command failed: %v", err)
	}

	// 5. Restore with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "library", "restore", backupSnapshot)
	if err != nil {
		t.Fatalf("restore --no-backup failed: %v", err)
	}

	// 6. Restore failure with bad backup path
	_, err = executeCommand("-l", libDir, "library", "restore", "/nonexistent/backup")
	if err == nil {
		t.Error("expected error for nonexistent restore path")
	}
}

func TestBackupHelpExplainsSnapshotConsistency(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			out, err := executeCommand("library", "backup", "--help")
			if err != nil {
				t.Fatal(err)
			}
			for _, term := range []string{"committed", "independently"} {
				if !strings.Contains(out, term) {
					t.Fatalf("backup help missing %q: %s", term, out)
				}
			}
		})
	}
}

func TestLibraryVerifyCommand(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Verify plain output (healthy)
	out, err := executeCommand("-l", libDir, "library", "verify")
	if err != nil {
		t.Fatalf("verify command failed: %v", err)
	}
	if strings.Contains(out, "===") {
		t.Errorf("verify output should not contain static headers '===', got: %s", out)
	}

	// 2. Verify JSON output
	_, err = executeCommand("-l", libDir, "library", "verify", "--json")
	if err != nil {
		t.Fatalf("verify --json failed: %v", err)
	}

	// 3. Verify with detected issues
	dbPath := filepath.Join(libDir, "Database2", "m.db")
	func() {
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("open verification fixture database: %v", err)
		}
		defer func() {
			if err := database.Close(); err != nil {
				t.Fatalf("close verification fixture database: %v", err)
			}
		}()
		if _, err := database.ExecContext(t.Context(), "INSERT INTO Track (id, path, filename, fileBytes) VALUES (999, 'Music/missing.m4a', 'missing.m4a', 100);"); err != nil {
			t.Fatalf("insert missing-audio fixture track: %v", err)
		}
	}()

	_, err = executeCommand("-l", libDir, "library", "verify")
	if err != nil {
		t.Fatalf("verify with issues failed: %v", err)
	}

	// 4. Verify on bad database path
	_, err = executeCommand("-l", "/nonexistent/lib", "library", "verify")
	if err == nil {
		t.Error("expected error for nonexistent lib in verify")
	}
}

func TestLibrarySyncAndPruneCommand(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Sync success (with unindexed file)
	_, err := executeCommand("-l", libDir, "library", "refresh")
	if err != nil {
		t.Fatalf("sync command failed: %v", err)
	}

	// 2. Sync with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "library", "refresh")
	if err != nil {
		t.Fatalf("sync --no-backup failed: %v", err)
	}

	// 3. Prune
	_, err = executeCommand("-l", libDir, "library", "prune")
	if err != nil {
		t.Fatalf("library prune failed: %v", err)
	}

	// 4. Prune with --no-backup
	_, err = executeCommand("-l", libDir, "--no-backup", "library", "prune")
	if err != nil {
		t.Fatalf("library prune --no-backup failed: %v", err)
	}

	// 5. Errors on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "library", "refresh")
	if err == nil {
		t.Error("expected error for nonexistent lib in sync")
	}
	_, err = executeCommand("-l", "/nonexistent/lib", "library", "prune")
	if err == nil {
		t.Error("expected error for nonexistent lib in library prune")
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
	if !strings.Contains(inspectOut, "track: 1 artist: Sample Artist title: Sample Title") {
		t.Errorf("expected track line 'Sample Artist - Sample Title [id:1 5:00]', got: %s", inspectOut)
	}

	// 2b. Inspect empty playlist (EDM Folder ID 1)
	emptyInspectOut, err := executeCommand("-l", libDir, "playlist", "inspect", "1")
	if err != nil {
		t.Fatalf("playlist inspect 1 failed: %v", err)
	}
	if !strings.Contains(emptyInspectOut, "playlist: 1 title: EDM Folder") {
		t.Errorf("expected 'No tracks in playlist.', got: %s", emptyInspectOut)
	}

	// 3. Inspect non-existent ID
	missingOut, err := executeCommand("-l", libDir, "playlist", "inspect", "99999")
	if err != nil || !strings.Contains(missingOut, "error: not_found") {
		t.Error("expected per-item error for nonexistent playlist ID")
	}

	// 4. List error on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "playlist", "list")
	if err == nil {
		t.Error("expected error for nonexistent lib in playlist list")
	}

	// 5. Inspect error on bad lib path
	_, err = executeCommand("-l", "/nonexistent/lib", "playlist", "inspect", "1")
	if err == nil {
		t.Error("expected error for nonexistent lib in playlist inspect")
	}

	// 6. Empty playlist list
	emptyDir := t.TempDir()
	emptyDBPath := filepath.Join(emptyDir, "Database2", "m.db")
	createEmptyTestDatabase(t, emptyDBPath)

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
	vOut, err := executeCommand("-l", libDir, "library", "verify")
	if err != nil {
		t.Fatalf("verify in agent mode failed: %v", err)
	}
	if !strings.Contains(vOut, "total_tracks: 1") || !strings.Contains(vOut, "status: ") {
		t.Errorf("unexpected agent verify output: %s", vOut)
	}

	// 2. Backup in AGENT mode
	bOut, err := executeCommand("-l", libDir, "library", "backup", backupOutDir)
	if err != nil {
		t.Fatalf("backup in agent mode failed: %v", err)
	}
	if !strings.Contains(bOut, "status: success") || !strings.Contains(bOut, "db_files: 2") {
		t.Errorf("unexpected agent backup output: %s", bOut)
	}

	// 3. Backup list in AGENT mode
	blOut, err := executeCommand("-l", libDir, "library", "backup", backupOutDir, "--list")
	if err != nil {
		t.Fatalf("backup list in agent mode failed: %v", err)
	}
	if !strings.Contains(blOut, "count: 1") || !strings.Contains(blOut, "backup: ") {
		t.Errorf("unexpected agent backup list output: %s", blOut)
	}

	// 4. Restore in AGENT mode
	entries, err := os.ReadDir(backupOutDir)
	if err != nil {
		t.Fatalf("read agent fixture backup directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no agent fixture backup created")
	}
	snapshot := filepath.Join(backupOutDir, entries[0].Name())
	rOut, err := executeCommand("-l", libDir, "library", "restore", snapshot)
	if err != nil {
		t.Fatalf("restore in agent mode failed: %v", err)
	}
	if !strings.Contains(rOut, "status: success") || !strings.Contains(rOut, "restored_from: ") {
		t.Errorf("unexpected agent restore output: %s", rOut)
	}

	// 5. Track artwork normalize in AGENT mode
	anOut, err := executeCommand("-l", libDir, "track", "artwork", "normalize")
	if err != nil {
		t.Fatalf("artwork normalize in agent mode failed: %v", err)
	}
	if !strings.Contains(anOut, "status: complete") || !strings.Contains(anOut, "total_tracks: 1") {
		t.Errorf("unexpected agent artwork normalize output: %s", anOut)
	}

	// 6. Library prune in AGENT mode
	apOut, err := executeCommand("-l", libDir, "library", "prune")
	if err != nil {
		t.Fatalf("library prune in agent mode failed: %v", err)
	}
	if !strings.Contains(apOut, "status: complete") || !strings.Contains(apOut, "deleted_db_rows: ") {
		t.Errorf("unexpected agent library prune output: %s", apOut)
	}

	// 7. Sync in AGENT mode
	sOut, err := executeCommand("-l", libDir, "library", "refresh")
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
	if !strings.Contains(piOut, "track: 1 artist: Sample Artist title: Sample Title") {
		t.Errorf("unexpected agent playlist inspect output: %s", piOut)
	}

	// 8b. Playlist inspect empty in AGENT mode
	piEmptyOut, err := executeCommand("-l", libDir, "playlist", "inspect", "1")
	if err != nil {
		t.Fatalf("playlist inspect 1 in agent mode failed: %v", err)
	}
	if !strings.Contains(piEmptyOut, "playlist: 1 title: EDM Folder") {
		t.Errorf("unexpected agent empty playlist inspect output: %s", piEmptyOut)
	}

	// 9. Playlist list empty in AGENT mode
	emptyDir := t.TempDir()
	emptyDBPath := filepath.Join(emptyDir, "Database2", "m.db")
	createEmptyTestDatabase(t, emptyDBPath)

	peOut, err := executeCommand("-l", emptyDir, "playlist", "list")
	if err != nil {
		t.Fatalf("empty playlist list in agent mode failed: %v", err)
	}
	if !strings.Contains(peOut, "status: empty") {
		t.Errorf("unexpected empty playlist output in agent mode: %s", peOut)
	}

	// 10. Backup list empty in AGENT mode
	emptyBackupDir := t.TempDir()
	beOut, err := executeCommand("-l", libDir, "library", "backup", emptyBackupDir, "--list")
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
		{"library unknown subcommand bar", []string{"-l", libDir, "library", "bar"}},
		{"root unknown command", []string{"-l", libDir, "unknown-cmd"}},
		{"library verify extra arg", []string{"-l", libDir, "library", "verify", "extra"}},
		{"library refresh extra arg", []string{"-l", libDir, "library", "refresh", "extra"}},
		{"playlist list extra arg", []string{"-l", libDir, "playlist", "list", "extra"}},
		{"track artwork normalize extra arg", []string{"-l", libDir, "track", "artwork", "normalize", "extra"}},
		{"library prune extra arg", []string{"-l", libDir, "library", "prune", "extra"}},
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
				"<id...>",
				"--json",
				"playlist IDs",
			},
		},
		{
			name: "library restore help",
			args: []string{"library", "restore", "--help"},
			wantSubstr: []string{
				"<path>",
				"Arguments:",
				"backup --list",
			},
		},
		{
			name: "track artwork normalize help",
			args: []string{"track", "artwork", "normalize", "--help"},
			wantSubstr: []string{
				"1:1 square",
				"--no-backup",
			},
		},
		{
			name: "library prune help",
			args: []string{"library", "prune", "--help"},
			wantSubstr: []string{
				"delete old, unneeded cover art",
				"--no-backup",
			},
		},
		{
			name: "library refresh help",
			args: []string{"library", "refresh", "--help"},
			wantSubstr: []string{
				"Scan your audio files",
				"--no-backup",
			},
		},
		{
			name: "library verify help",
			args: []string{"library", "verify", "--help"},
			wantSubstr: []string{
				"audit",
				"--json",
			},
		},
		{
			name: "library backup help",
			args: []string{"library", "backup", "--help"},
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
		{
			name: "playlist create help",
			args: []string{"playlist", "create", "--help"},
			wantSubstr: []string{
				"<name>",
				"--parent",
				"--folder",
			},
		},
		{
			name: "playlist rm help",
			args: []string{"playlist", "rm", "--help"},
			wantSubstr: []string{
				"<id>",
				"--force",
			},
		},
		{
			name: "playlist move help",
			args: []string{"playlist", "move", "--help"},
			wantSubstr: []string{
				"<id>",
				"--to",
			},
		},
		{
			name: "playlist track add help",
			args: []string{"playlist", "track", "add", "--help"},
			wantSubstr: []string{
				"<id> <id|path...>",
			},
		},
		{
			name: "playlist track rm help",
			args: []string{"playlist", "track", "rm", "--help"},
			wantSubstr: []string{
				"<id> <id|path...>",
			},
		},
		{
			name: "playlist track move help",
			args: []string{"playlist", "track", "move", "--help"},
			wantSubstr: []string{
				"<src-id> <dst-id> <id|path...>",
			},
		},
		{
			name: "track add help",
			args: []string{"track", "add", "--help"},
			wantSubstr: []string{
				"<path...>",
				"--to-playlist",
				"--move",
			},
		},
		{
			name: "track rm help",
			args: []string{"track", "rm", "--help"},
			wantSubstr: []string{
				"<id|path...>",
				"--delete-file",
			},
		},
		{
			name: "track move help",
			args: []string{"track", "move", "--help"},
			wantSubstr: []string{
				"<id|path> <dest-path>",
			},
		},
		{
			name: "track relocate help",
			args: []string{"track", "relocate", "--help"},
			wantSubstr: []string{
				"--from",
				"--to",
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
			name: "track artwork normalize in agent mode",
			args: []string{"track", "artwork", "normalize", "--help"},
			wantSubstr: []string{
				"command: engine-cli track artwork normalize",
				"AlbumArt",
				"mutates_db: true",
				"auto_backup: true",
			},
		},
		{
			name: "library verify in agent mode",
			args: []string{"library", "verify", "--help"},
			wantSubstr: []string{
				"command: engine-cli library verify",
				"Database2/m.db",
				"Track, Playlist",
			},
		},
		{
			name: "playlist inspect in agent mode",
			args: []string{"playlist", "inspect", "--help"},
			wantSubstr: []string{
				"command: engine-cli playlist inspect",
				"args:\n  - <id...>",
			},
		},
		{
			name: "track add in agent mode",
			args: []string{"track", "add", "--help"},
			wantSubstr: []string{
				"command: engine-cli track add",
				"mutates_db: true",
				"auto_backup: true",
			},
		},
		{
			name: "playlist track move in agent mode",
			args: []string{"playlist", "track", "move", "--help"},
			wantSubstr: []string{
				"command: engine-cli playlist track move",
				"mutates_db: true",
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

func TestPlaylistMoveBeforeSibling(t *testing.T) {
	t.Setenv("AGENT", "1")
	lib := createTestLibrary(t)
	if _, err := executeCommand("-l", lib, "playlist", "create", "Later"); err != nil {
		t.Fatal(err)
	}
	if _, err := executeCommand("-l", lib, "playlist", "move", "3", "--to", "0", "--before", "1"); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			t.Fatalf("close playlist-order fixture database: %v", err)
		}
	}()
	lists, err := db.GetAllPlaylists(t.Context(), d)
	if err != nil || len(lists) != 3 || lists[0].ID != 3 || lists[1].ID != 1 {
		t.Fatalf("lists=%v err=%v", lists, err)
	}
	if _, err := executeCommand("-l", lib, "playlist", "move", "3", "--to", "0", "--before", "2"); err == nil {
		t.Fatal("accepted a sibling from another folder")
	}
}

func TestNewPlaylistCommands_E2E(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// 1. Create playlist
	outCreate, err := executeCommand("-l", libDir, "playlist", "create", "Melodic House")
	if err != nil {
		t.Fatalf("playlist create failed: %v", err)
	}
	if !strings.Contains(outCreate, `[OK] Created playlist "Melodic House"`) {
		t.Errorf("unexpected create output: %s", outCreate)
	}

	// 2. Create folder under parent
	outFolder, err := executeCommand("-l", libDir, "playlist", "create", "Subgenres", "--parent", "1", "--folder")
	if err != nil {
		t.Fatalf("playlist create folder failed: %v", err)
	}
	if !strings.Contains(outFolder, `[OK] Created folder "Subgenres"`) {
		t.Errorf("unexpected folder create output: %s", outFolder)
	}

	// 3. Move playlist
	outMove, err := executeCommand("-l", libDir, "playlist", "move", "3", "--to", "4")
	if err != nil {
		t.Fatalf("playlist move failed: %v", err)
	}
	if !strings.Contains(outMove, `[OK] Moved playlist "Melodic House"`) {
		t.Errorf("unexpected move output: %s", outMove)
	}

	// 4. Playlist track add
	outAddTrack, err := executeCommand("-l", libDir, "playlist", "track", "add", "3", "1")
	if err != nil {
		t.Fatalf("playlist track add failed: %v", err)
	}
	if !strings.Contains(outAddTrack, `[OK] Added track "Sample Title"`) {
		t.Errorf("unexpected track add output: %s", outAddTrack)
	}

	// 5. Playlist track move
	outMoveTrack, err := executeCommand("-l", libDir, "playlist", "track", "move", "3", "2", "1")
	if err != nil {
		t.Fatalf("playlist track move failed: %v", err)
	}
	if !strings.Contains(outMoveTrack, `[OK] Moved track "Sample Title"`) {
		t.Errorf("unexpected track move output: %s", outMoveTrack)
	}

	// 6. Playlist track rm
	outRmTrack, err := executeCommand("-l", libDir, "playlist", "track", "rm", "2", "1")
	if err != nil {
		t.Fatalf("playlist track rm failed: %v", err)
	}
	if !strings.Contains(outRmTrack, `[OK] Removed track "Sample Title"`) {
		t.Errorf("unexpected track rm output: %s", outRmTrack)
	}

	// 7. Playlist rm
	outRmPL, err := executeCommand("-l", libDir, "playlist", "rm", "3")
	if err != nil {
		t.Fatalf("playlist rm failed: %v", err)
	}
	if !strings.Contains(outRmPL, `[OK] Deleted playlist "Melodic House"`) {
		t.Errorf("unexpected playlist rm output: %s", outRmPL)
	}
}

func TestNewTrackCommands_E2E(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// Create a new audio file
	newAudio := filepath.Join(libDir, "incoming.mp3")
	if err := os.WriteFile(newAudio, []byte("incoming track dummy bytes"), 0o644); err != nil {
		t.Fatalf("write incoming fixture audio: %v", err)
	}

	// 1. Track add with --to-playlist and --move
	outAdd, err := executeCommand("-l", libDir, "track", "add", "--to-playlist", "2", "--move", newAudio)
	if err != nil {
		t.Fatalf("track add failed: %v", err)
	}
	if !strings.Contains(outAdd, `[OK] Added track "incoming"`) || !strings.Contains(outAdd, "Assigned to playlist") {
		t.Errorf("unexpected track add output: %s", outAdd)
	}

	// 2. Track move
	destPath := filepath.Join(libDir, "Music", "Custom", "relocated_song.mp3")
	outMove, err := executeCommand("-l", libDir, "track", "move", "2", destPath)
	if err != nil {
		t.Fatalf("track move failed: %v", err)
	}
	if !strings.Contains(outMove, `[OK] Moved track "incoming"`) {
		t.Errorf("unexpected track move output: %s", outMove)
	}

	// 3. Track relocate
	outRelocate, err := executeCommand("-l", libDir, "track", "relocate", "--from", "Music/Custom", "--to", "Music/Moved")
	if err != nil {
		t.Fatalf("track relocate failed: %v", err)
	}
	if !strings.Contains(outRelocate, `[OK] Relocated 1 track(s)`) {
		t.Errorf("unexpected relocate output: %s", outRelocate)
	}

	// 4. Track rm with --delete-file
	outRm, err := executeCommand("-l", libDir, "track", "rm", "2", "--delete-file")
	if err != nil {
		t.Fatalf("track rm failed: %v", err)
	}
	if !strings.Contains(outRm, `[OK] Removed track "incoming"`) || !strings.Contains(outRm, "Deleted audio file from disk") {
		t.Errorf("unexpected track rm output: %s", outRm)
	}
}

func TestAgentMode_NewCommands(t *testing.T) {
	t.Setenv("AGENT", "1")
	libDir := createTestLibrary(t)

	// 1. Playlist create in AGENT mode
	cOut, err := executeCommand("-l", libDir, "playlist", "create", "AgentPL")
	if err != nil {
		t.Fatalf("playlist create in agent mode failed: %v", err)
	}
	if !strings.Contains(cOut, "status: ok") || !strings.Contains(cOut, "title: AgentPL") {
		t.Errorf("unexpected agent create output: %s", cOut)
	}

	// 2. Playlist move in AGENT mode
	mOut, err := executeCommand("-l", libDir, "playlist", "move", "3", "--to", "1")
	if err != nil {
		t.Fatalf("playlist move in agent mode failed: %v", err)
	}
	if !strings.Contains(mOut, "status: ok") || !strings.Contains(mOut, "parent_id: 1") {
		t.Errorf("unexpected agent move output: %s", mOut)
	}

	// 3. Playlist track add in AGENT mode
	ptaOut, err := executeCommand("-l", libDir, "playlist", "track", "add", "3", "1")
	if err != nil {
		t.Fatalf("playlist track add in agent mode failed: %v", err)
	}
	if !strings.Contains(ptaOut, "status: ok") || !strings.Contains(ptaOut, "track_id: 1") {
		t.Errorf("unexpected agent playlist track add output: %s", ptaOut)
	}

	// 4. Playlist track move in AGENT mode
	ptmOut, err := executeCommand("-l", libDir, "playlist", "track", "move", "3", "2", "1")
	if err != nil {
		t.Fatalf("playlist track move in agent mode failed: %v", err)
	}
	if !strings.Contains(ptmOut, "status: ok") || !strings.Contains(ptmOut, "dst_playlist_id: 2") {
		t.Errorf("unexpected agent playlist track move output: %s", ptmOut)
	}

	// 5. Playlist track rm in AGENT mode
	ptrOut, err := executeCommand("-l", libDir, "playlist", "track", "rm", "2", "1")
	if err != nil {
		t.Fatalf("playlist track rm in agent mode failed: %v", err)
	}
	if !strings.Contains(ptrOut, "status: ok") || !strings.Contains(ptrOut, "removed_track_id: 1") {
		t.Errorf("unexpected agent playlist track rm output: %s", ptrOut)
	}

	// 6. Playlist rm in AGENT mode
	prmOut, err := executeCommand("-l", libDir, "playlist", "rm", "3")
	if err != nil {
		t.Fatalf("playlist rm in agent mode failed: %v", err)
	}
	if !strings.Contains(prmOut, "status: ok") || !strings.Contains(prmOut, "deleted_title: AgentPL") {
		t.Errorf("unexpected agent playlist rm output: %s", prmOut)
	}

	// 7. Track add in AGENT mode
	newAudio := filepath.Join(libDir, "agent_song.mp3")
	if err := os.WriteFile(newAudio, []byte("agent song data"), 0o644); err != nil {
		t.Fatalf("write agent fixture audio: %v", err)
	}
	taOut, err := executeCommand("-l", libDir, "track", "add", newAudio)
	if err != nil {
		t.Fatalf("track add in agent mode failed: %v", err)
	}
	if !strings.Contains(taOut, "status: ok") || !strings.Contains(taOut, "title: agent_song") {
		t.Errorf("unexpected agent track add output: %s", taOut)
	}

	// 8. Track move in AGENT mode
	destPath := filepath.Join(libDir, "Music", "agent_moved.mp3")
	tmOut, err := executeCommand("-l", libDir, "track", "move", "2", destPath)
	if err != nil {
		t.Fatalf("track move in agent mode failed: %v", err)
	}
	if !strings.Contains(tmOut, "status: ok") || !strings.Contains(tmOut, "new_path: Music/agent_moved.mp3") {
		t.Errorf("unexpected agent track move output: %s", tmOut)
	}

	// 9. Track relocate in AGENT mode
	trOut, err := executeCommand("-l", libDir, "track", "relocate", "--from", "Music/agent_moved.mp3", "--to", "Music/agent_relocated.mp3")
	if err != nil {
		t.Fatalf("track relocate in agent mode failed: %v", err)
	}
	if !strings.Contains(trOut, "status: ok") || !strings.Contains(trOut, "relocated_count: 1") {
		t.Errorf("unexpected agent track relocate output: %s", trOut)
	}

	// 10. Track rm in AGENT mode
	trmOut, err := executeCommand("-l", libDir, "track", "rm", "2")
	if err != nil {
		t.Fatalf("track rm in agent mode failed: %v", err)
	}
	if !strings.Contains(trmOut, "status: ok") || !strings.Contains(trmOut, "title: agent_song") {
		t.Errorf("unexpected agent track rm output: %s", trmOut)
	}
}

func TestCommandHelpAndErrorBranches(t *testing.T) {
	t.Setenv("AGENT", "0")
	libDir := createTestLibrary(t)

	// Parent commands without subcommands trigger help
	for _, parent := range [][]string{
		{"library"},
		{"playlist"},
		{"playlist", "track"},
		{"track"},
		{"track", "artwork"},
	} {
		out, err := executeCommand(parent...)
		if err != nil {
			t.Fatalf("parent command %v returned error: %v", parent, err)
		}
		if !strings.Contains(out, "Usage:") && !strings.Contains(out, "Flags:") {
			t.Errorf("expected usage help for %v, got: %s", parent, out)
		}
	}

	// Commands with --no-backup
	newAudio := filepath.Join(libDir, "nobackup.mp3")
	if err := os.WriteFile(newAudio, []byte("data"), 0o644); err != nil {
		t.Fatalf("write no-backup fixture audio: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "track", "add", newAudio); err != nil {
		t.Fatalf("track add --no-backup error: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "playlist", "create", "NoBackupPL"); err != nil {
		t.Fatalf("playlist create --no-backup error: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "playlist", "track", "add", "3", "2"); err != nil {
		t.Fatalf("playlist track add --no-backup error: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "playlist", "track", "rm", "3", "2"); err != nil {
		t.Fatalf("playlist track rm --no-backup error: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "playlist", "move", "3", "--to", "0"); err != nil {
		t.Fatalf("playlist move --no-backup error: %v", err)
	}
	if _, err := executeCommand("-l", libDir, "--no-backup", "playlist", "rm", "3"); err != nil {
		t.Fatalf("playlist rm --no-backup error: %v", err)
	}

	// Bad DB path errors for all commands
	badLib := "/nonexistent/path/lib"
	if _, err := executeCommand("-l", badLib, "library", "verify"); err == nil {
		t.Error("expected error for bad lib in library verify")
	}
	if _, err := executeCommand("-l", badLib, "library", "refresh"); err == nil {
		t.Error("expected error for bad lib in library refresh")
	}
	if _, err := executeCommand("-l", badLib, "library", "backup"); err == nil {
		t.Error("expected error for bad lib in library backup")
	}
	if _, err := executeCommand("-l", badLib, "library", "prune"); err == nil {
		t.Error("expected error for bad lib in library prune")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "create", "Bad"); err == nil {
		t.Error("expected error for bad lib in playlist create")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "rm", "Bad"); err == nil {
		t.Error("expected error for bad lib in playlist rm")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "move", "Bad", "--to", "0"); err == nil {
		t.Error("expected error for bad lib in playlist move")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "track", "add", "Bad", "1"); err == nil {
		t.Error("expected error for bad lib in playlist track add")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "track", "rm", "Bad", "1"); err == nil {
		t.Error("expected error for bad lib in playlist track rm")
	}
	if _, err := executeCommand("-l", badLib, "playlist", "track", "move", "Bad", "Dest", "1"); err == nil {
		t.Error("expected error for bad lib in playlist track move")
	}
	if _, err := executeCommand("-l", badLib, "track", "add", newAudio); err == nil {
		t.Error("expected error for bad lib in track add")
	}
	if _, err := executeCommand("-l", badLib, "track", "rm", "1"); err == nil {
		t.Error("expected error for bad lib in track rm")
	}
	if _, err := executeCommand("-l", badLib, "track", "move", "1", "dest"); err == nil {
		t.Error("expected error for bad lib in track move")
	}
	if _, err := executeCommand("-l", badLib, "track", "relocate", "--from", "a", "--to", "b"); err == nil {
		t.Error("expected error for bad lib in track relocate")
	}
	if _, err := executeCommand("-l", badLib, "track", "artwork", "normalize"); err == nil {
		t.Error("expected error for bad lib in track artwork normalize")
	}
}
