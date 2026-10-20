package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/migrate/sync"
)

func TestSmartCopier_CopyFile(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	dstDir := filepath.Join(tempDir, "dst")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatalf("failed to create dst dir: %v", err)
	}

	srcFile := filepath.Join(srcDir, "test_track.mp3")
	content := []byte("audio-data-payload-1234567890")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}

	dstFile := filepath.Join(dstDir, "test_track.mp3")

	copier := sync.NewCopier()

	// 1. First copy should perform actual file copy
	copied, skipped, err := copier.CopyFile(context.Background(), srcFile, dstFile, nil)
	if err != nil {
		t.Fatalf("expected no error on copy, got: %v", err)
	}
	if !copied || skipped {
		t.Fatalf("expected copied=true, skipped=false, got copied=%v, skipped=%v", copied, skipped)
	}

	// Verify content matches
	dstContent, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read dst file: %v", err)
	}
	if string(dstContent) != string(content) {
		t.Fatalf("expected dst content %q, got %q", string(content), string(dstContent))
	}

	// 2. Second copy of identical file should smart-skip
	copied, skipped, err = copier.CopyFile(context.Background(), srcFile, dstFile, nil)
	if err != nil {
		t.Fatalf("expected no error on second copy, got: %v", err)
	}
	if copied || !skipped {
		t.Fatalf("expected copied=false, skipped=true on re-copy, got copied=%v, skipped=%v", copied, skipped)
	}
}

func TestSmartCopier_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "large_file.bin")
	dstFile := filepath.Join(tempDir, "dst_large_file.bin")

	// Create a larger file
	data := make([]byte, 1024*1024) // 1MB
	if err := os.WriteFile(srcFile, data, 0644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	copier := sync.NewCopier()
	_, _, err := copier.CopyFile(ctx, srcFile, dstFile, nil)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}

	// Ensure incomplete file was cleaned up
	if _, err := os.Stat(dstFile); !os.IsNotExist(err) {
		t.Fatalf("expected destination file to be cleaned up on cancellation")
	}
}
