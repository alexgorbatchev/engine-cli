//go:build unix

package regularfile

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRetainedFileSurvivesPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "original" {
		t.Fatalf("retained identity data=%q err=%v", data, err)
	}
	conn, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var flagErr error
	if err := conn.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
		t.Fatal(err)
	}
	if flagErr != nil || flags&unix.O_NONBLOCK != 0 {
		t.Fatalf("retained file is nonblocking: flags=%d err=%v", flags, flagErr)
	}
}

func TestRootPreservesSymlinkConfinement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audio"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("audio", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := OpenRoot(root, "link")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if file, err := OpenRoot(root, "escape/missing"); err == nil || file != nil {
		t.Fatalf("escaped root: file=%v err=%v", file, err)
	}
}
