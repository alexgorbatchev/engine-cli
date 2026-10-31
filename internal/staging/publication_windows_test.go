package staging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
