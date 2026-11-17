package testtemp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUsePhysicalResolvesASymlinkedTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows takes its temporary directory from TMP and TEMP")
	}
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", link)

	if err := UsePhysical(); err != nil {
		t.Fatal(err)
	}
	if got := os.TempDir(); got != physical {
		t.Fatalf("os.TempDir() = %q, want %q", got, physical)
	}
}

func TestUsePhysicalReportsAMissingTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows takes its temporary directory from TMP and TEMP")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)

	if err := UsePhysical(); err == nil {
		t.Fatal("UsePhysical() succeeded for a missing temporary directory")
	}
	if got := os.Getenv("TMPDIR"); got != missing {
		t.Fatalf("TMPDIR = %q, want it unchanged", got)
	}
}
