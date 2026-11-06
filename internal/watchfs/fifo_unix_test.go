//go:build linux || darwin

package watchfs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFIFORefusesWithoutPeer(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	if file, err := dir.OpenFile("fifo"); err == nil {
		_ = file.Close()
		t.Fatal("accepted FIFO")
	}
	if _, err := os.Stat(filepath.Join(root, "fifo")); err != nil {
		t.Fatal("source removed on refusal:", err)
	}
}
