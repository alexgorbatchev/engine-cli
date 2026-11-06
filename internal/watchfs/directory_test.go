package watchfs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedDirectoryReads(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	want := []byte("original native source")
	if err := os.WriteFile(filepath.Join(root, "nested", "source.wav"), want, 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	identity, err := dir.Identity()
	if err != nil || identity == "" {
		t.Fatalf("retained root identity: %q %v", identity, err)
	}
	again, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	observed, identityErr := again.Identity()
	if err := errors.Join(identityErr, again.Close()); err != nil || observed != identity {
		t.Fatalf("same native root identity: %q != %q: %v", observed, identity, err)
	}
	for range 2 {
		entries, err := dir.Entries()
		if err != nil || len(entries) != 1 || entries[0].Name() != "nested" {
			t.Fatalf("fresh enumeration: %v, %v", entries, err)
		}
	}
	child, err := dir.OpenDirectory("nested")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	file, err := child.OpenFile("source.wav")
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close()); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q: %v", got, err)
	}
	if _, err := child.OpenDirectory("source.wav"); err == nil {
		t.Fatal("ordinary file accepted as directory")
	}
	if _, err := dir.OpenFile("nested"); err == nil {
		t.Fatal("directory accepted as source file")
	}
	for _, name := range []string{"", ".", "..", "nested/source.wav", "bad\x00name", filepath.Join(root, "nested")} {
		if _, err := dir.OpenFile(name); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("unsafe component %q: %v", name, err)
		}
	}
}

func TestRetainedDirectoryReplacementRefuses(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	if err := os.Rename(root, filepath.Join(base, "previous")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "replacement.wav"), []byte("unapproved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := dir.Check(); err == nil {
		t.Fatal("replaced authorization repinned")
	}
	if _, err := dir.Entries(); err == nil {
		t.Fatal("enumerated replacement directory")
	}
	if _, err := dir.OpenFile("replacement.wav"); err == nil {
		t.Fatal("opened replacement source")
	}
}

func TestClosedDirectoryRefuses(t *testing.T) {
	dir, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Check(); err != nil {
		t.Fatal(err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	_, cause := dir.file.Stat()
	if cause == nil {
		t.Fatal("closed native directory still observable")
	}
	var native *os.PathError
	if !errors.As(cause, &native) {
		t.Fatalf("native closed Stat cause: %v", cause)
	}
	cause = native.Err
	if _, err := dir.Identity(); err == nil {
		t.Fatalf("closed retained identity: %v", err)
	}
	if err := dir.Check(); !errors.Is(err, cause) {
		t.Fatalf("closed identity observation: %v", err)
	}
	if _, err := dir.Entries(); !errors.Is(err, cause) {
		t.Fatalf("closed enumeration: %v", err)
	}
	if _, err := dir.OpenFile("source.wav"); !errors.Is(err, cause) {
		t.Fatalf("closed source open: %v", err)
	}
}

func TestUnavailableAuthorizationRefuses(t *testing.T) {
	base := t.TempDir()
	ordinary := filepath.Join(base, "ordinary")
	if err := os.WriteFile(ordinary, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(base, "missing"), ordinary, filepath.Join(ordinary, "child"), "bad\x00root"} {
		if dir, err := Open(path); err == nil {
			_ = dir.Close()
			t.Fatalf("unavailable directory accepted: %q", path)
		}
	}
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	if _, err := dir.OpenFile("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source cause: %v", err)
	}
	if err := os.Rename(root, filepath.Join(base, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := dir.Check(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing authorized root: %v", err)
	}
	if _, err := dir.Entries(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root enumeration: %v", err)
	}
}
