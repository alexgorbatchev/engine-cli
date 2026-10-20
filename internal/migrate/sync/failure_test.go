package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyRenameFailureLeavesOriginalDestination(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	writeAudio(t, src, []byte("audio"))
	_, _, err := NewCopier().CopyFile(context.Background(), src, dst, func(_, _ int64) {
		if err := os.Mkdir(dst, 0755); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("failed commit was reported successful")
	}
	info, err := os.Stat(dst)
	if err != nil || !info.IsDir() {
		t.Fatalf("destination overwritten: %v", err)
	}
	if _, err := fileDigest(context.Background(), dir); err == nil {
		t.Fatal("directory was hashed as an audio file")
	}
}

func TestUnreadableAudioAndUnwritableTargets(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem permissions")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	writeAudio(t, src, []byte("audio"))
	writeAudio(t, dst, []byte("audio"))
	for _, path := range []string{src, dst} {
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		if _, _, err := NewCopier().CopyFile(context.Background(), src, dst, nil); err == nil {
			t.Fatal("unreadable audio accepted")
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	if _, _, err := NewCopier().CopyFile(context.Background(), src, filepath.Join(dir, "new"), nil); err == nil {
		t.Fatal("unwritable destination accepted")
	}
	if err := WriteAtomic(filepath.Join(dir, "state"), []byte("x")); err == nil {
		t.Fatal("unwritable metadata accepted")
	}
}
