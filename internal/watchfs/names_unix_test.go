//go:build linux || darwin

package watchfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeUnixComponentPreservesBackslashFilename(t *testing.T) {
	root := t.TempDir()
	name := `source\take.wav`
	want := "native backslash filename bytes"
	if err := os.WriteFile(filepath.Join(root, name), []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	file, err := dir.OpenFile(name)
	if err != nil {
		t.Fatal("valid native single component refused:", err)
	}
	got, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close()); err != nil || string(got) != want {
		t.Fatalf("native component bytes: %q %v", got, err)
	}
}
