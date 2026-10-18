package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func createDummyEngineLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create Database2/*.db
	db2 := filepath.Join(dir, "Database2")
	_ = os.MkdirAll(db2, 0755)
	createSnapshotFixture(t, filepath.Join(db2, "m.db"), "sample m.db content")
	createSnapshotFixture(t, filepath.Join(db2, "stm.db"), "sample stm.db content")

	// Create root *.db
	createSnapshotFixture(t, filepath.Join(dir, "m.db"), "sample root m.db")

	// Create Artwork/
	art := filepath.Join(dir, "Artwork")
	_ = os.MkdirAll(art, 0755)
	_ = os.WriteFile(filepath.Join(art, "sample_art.jpg"), []byte("sample art bytes"), 0644)

	return dir
}

func TestCreateAndRestoreBackup(t *testing.T) {
	ctx := context.Background()
	engineDir := createDummyEngineLibrary(t)
	backupOutDir := t.TempDir()

	// 1. Create backup
	info, err := CreateBackup(ctx, engineDir, backupOutDir)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	if info == nil || info.DBFiles != 3 {
		t.Fatalf("expected 3 db files in backup, got %v", info)
	}
	if info.ArtFiles != 1 {
		t.Errorf("expected 1 art file in backup, got %d", info.ArtFiles)
	}

	// 2. List backups
	backups, err := ListBackups("", backupOutDir)
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup listed, got %d", len(backups))
	}
	if backups[0].Name != info.Name {
		t.Errorf("expected backup name %q, got %q", info.Name, backups[0].Name)
	}

	// 3. Corrupt/Modify engineDir
	_ = os.Remove(filepath.Join(engineDir, "Database2", "m.db"))
	_ = os.WriteFile(filepath.Join(engineDir, "Database2", "m.db"), []byte("corrupted"), 0644)

	// 4. Restore backup
	restoreDir := info.Path
	err = RestoreBackup(ctx, restoreDir, engineDir)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	restored, err := sql.Open("sqlite", filepath.Join(engineDir, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "sample m.db content" {
		t.Errorf("expected restored content, got %q, error %v", value, err)
	}
}

func TestGetDefaultBackupDir_AndAutoBackup(t *testing.T) {
	ctx := context.Background()
	lib := createDummyEngineLibrary(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, err := GetDefaultBackupDir(lib)
	if err != nil || dir != filepath.Join(lib, ".engine-cli", "backups") {
		t.Fatalf("default %s: %v", dir, err)
	}
	skipped, err := AutoBackupIfNeeded(ctx, lib, true)
	if err != nil || skipped != nil {
		t.Fatalf("opt out: %v %v", skipped, err)
	}
	for range 2 {
		info, err := AutoBackupIfNeeded(ctx, lib, false)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(info.Path) != dir || info.DBFiles != 3 || info.ArtFiles != 0 {
			t.Fatalf("snapshot: %+v", info)
		}
	}
	list, err := ListBackups(lib, "")
	if err != nil || len(list) != 2 {
		t.Fatalf("listing %v: %v", list, err)
	}
}

func TestBackup_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Empty engine library directory
	_, err := CreateBackup(ctx, "", tempDir)
	if err == nil {
		t.Error("expected error for empty engineLibDir")
	}

	// 2. Non-existent engine library directory
	_, err = CreateBackup(ctx, filepath.Join(tempDir, "nonexistent"), tempDir)
	if err == nil {
		t.Error("expected error for nonexistent engineLibDir")
	}

	// 3. Empty restore params
	if err := RestoreBackup(ctx, "", tempDir); err == nil {
		t.Error("expected error for empty backupDir")
	}
	if err := RestoreBackup(ctx, tempDir, ""); err == nil {
		t.Error("expected error for empty engineLibDir")
	}

	// 4. Non-existent restore paths
	if err := RestoreBackup(ctx, filepath.Join(tempDir, "nonexistent"), tempDir); err == nil {
		t.Error("expected error for nonexistent backupDir")
	}
	if err := RestoreBackup(ctx, tempDir, filepath.Join(tempDir, "nonexistent")); err == nil {
		t.Error("expected error for nonexistent engineLibDir")
	}

	// 5. ListBackups on non-existent path
	nonExistentDir := filepath.Join(tempDir, "nonexistent-dir")
	list, err := ListBackups("", nonExistentDir)
	if err != nil {
		t.Errorf("expected non-existent dir to return empty list without error, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 backups, got %d", len(list))
	}
}
