package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createDummyEngineLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create Database2/*.db
	db2 := filepath.Join(dir, "Database2")
	_ = os.MkdirAll(db2, 0755)
	_ = os.WriteFile(filepath.Join(db2, "m.db"), []byte("sample m.db content"), 0644)
	_ = os.WriteFile(filepath.Join(db2, "stm.db"), []byte("sample stm.db content"), 0644)

	// Create root *.db
	_ = os.WriteFile(filepath.Join(dir, "m.db"), []byte("sample root m.db"), 0644)

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
	backups, err := ListBackups(backupOutDir)
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

	restoredContent, _ := os.ReadFile(filepath.Join(engineDir, "Database2", "m.db"))
	if string(restoredContent) != "sample m.db content" {
		t.Errorf("expected restored content, got %q", string(restoredContent))
	}
}

func TestGetDefaultBackupDir_AndAutoBackup(t *testing.T) {
	ctx := context.Background()
	engineDir := createDummyEngineLibrary(t)
	tempXDG := t.TempDir()

	t.Setenv("XDG_DATA_HOME", tempXDG)
	dir, err := GetDefaultBackupDir()
	if err != nil {
		t.Fatalf("GetDefaultBackupDir failed: %v", err)
	}
	wantPrefix := filepath.Join(tempXDG, "engine-cli", "backups")
	if dir != wantPrefix {
		t.Errorf("expected %q, got %q", wantPrefix, dir)
	}

	// AutoBackup with noBackup = true
	infoNoBk, err := AutoBackupIfNeeded(ctx, engineDir, true)
	if err != nil {
		t.Fatalf("AutoBackupIfNeeded(noBackup=true) failed: %v", err)
	}
	if infoNoBk != nil {
		t.Errorf("expected nil backup info, got %v", infoNoBk)
	}

	// AutoBackup with noBackup = false (uses default XDG dir)
	infoBk, err := AutoBackupIfNeeded(ctx, engineDir, false)
	if err != nil {
		t.Fatalf("AutoBackupIfNeeded(noBackup=false) failed: %v", err)
	}
	if infoBk == nil || !strings.Contains(infoBk.Path, tempXDG) {
		t.Errorf("expected backup in XDG dir %q, got %v", tempXDG, infoBk)
	}

	// List backups with empty dir (should use default XDG dir)
	list, err := ListBackups("")
	if err != nil {
		t.Fatalf("ListBackups('') failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 backup listed in default XDG dir, got %d", len(list))
	}

	// Default backup with unset XDG_DATA_HOME
	_ = os.Unsetenv("XDG_DATA_HOME")
	dirFallback, errFallback := GetDefaultBackupDir()
	if errFallback != nil {
		t.Fatalf("GetDefaultBackupDir fallback failed: %v", errFallback)
	}
	if !strings.Contains(dirFallback, ".local/share/engine-cli/backups") && !strings.Contains(dirFallback, "engine-cli/backups") {
		t.Errorf("unexpected fallback dir: %q", dirFallback)
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
	list, err := ListBackups(nonExistentDir)
	if err != nil {
		t.Errorf("expected non-existent dir to return empty list without error, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 backups, got %d", len(list))
	}
}
