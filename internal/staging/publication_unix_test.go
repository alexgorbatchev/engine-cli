//go:build !windows

package staging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestPublicationRetainsRoot(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "staging")
	if err := os.MkdirAll(filepath.Join(path, "item"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tmp := filepath.Join("item", ".manifest-test")
	data := []byte("retained root manifest")
	if err := root.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(parent, "retained")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "item"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(path, "item", manifestName)
	old := []byte("replacement path must remain unchanged")
	if err := os.WriteFile(outside, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishManifest(root, "item", tmp); err != nil {
		t.Fatal(err)
	}
	assertManifestBytes(t, root, filepath.Join("item", manifestName), data)
	got, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("replacement path changed: %q, %v", got, err)
	}
}
