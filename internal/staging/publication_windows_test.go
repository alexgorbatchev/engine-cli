package staging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestManifestPublicationPinsRoot(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "staging")
	retained := filepath.Join(parent, "retained")
	if err := os.MkdirAll(filepath.Join(path, "item"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	tmp := filepath.Join("item", ".manifest-test")
	data := []byte("pinned root manifest")
	if err := root.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Windows OpenRoot deliberately omits FILE_SHARE_DELETE, pinning the root
	// against replacement for its lifetime rather than following a moved root.
	if err := os.Rename(path, retained); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("rename open root: %v; want sharing violation", err)
	}
	if _, err := os.Stat(retained); !os.IsNotExist(err) {
		t.Fatalf("failed rename created destination: %v", err)
	}
	assertManifestBytes(t, root, tmp, data)
	if err := publishManifest(root, "item", tmp); err != nil {
		t.Fatal(err)
	}
	assertManifestBytes(t, root, filepath.Join("item", manifestName), data)
	if _, err := root.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temporary manifest remains: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, retained); err != nil {
		t.Fatalf("rename after root close: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(retained, "item", manifestName))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("published manifest after rename: %q, %v", got, err)
	}
}

func TestManifestPublicationRejectsReparseSource(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("item", 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join("item", manifestName)
	old := []byte("existing complete manifest")
	if err := root.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	data := []byte("external source must remain unchanged")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join("item", ".manifest-test")
	if err := os.Symlink(outside, filepath.Join(path, tmp)); err != nil {
		t.Fatal(err)
	}
	if err := publishManifest(root, "item", tmp); err == nil {
		t.Fatal("published a source reparse point as the manifest")
	}
	assertManifestBytes(t, root, target, old)
	info, err := root.Lstat(tmp)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("source link changed: %v, %v", info, err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("external target changed: %q, %v", got, err)
	}
}
