package staging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestPublication(t *testing.T) {
	for _, existing := range []string{"absent", "file", "directory"} {
		t.Run(existing, func(t *testing.T) {
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
			old := []byte("old complete manifest")
			if existing == "file" {
				if err := root.WriteFile(target, old, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if existing == "directory" {
				if err := root.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := root.WriteFile(filepath.Join(target, "retained"), old, 0600); err != nil {
					t.Fatal(err)
				}
			}
			tmp := filepath.Join("item", ".manifest-test")
			data := []byte("new complete manifest")
			if err := root.WriteFile(tmp, data, 0600); err != nil {
				t.Fatal(err)
			}
			err = publishManifest(root, "item", tmp)
			if existing == "directory" {
				if err == nil {
					t.Fatal("replaced a directory with a manifest")
				}
				assertManifestBytes(t, root, filepath.Join(target, "retained"), old)
				assertManifestBytes(t, root, tmp, data)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertManifestBytes(t, root, target, data)
			if _, err := root.Stat(tmp); !os.IsNotExist(err) {
				t.Fatalf("temporary manifest remains: %v", err)
			}
		})
	}
}

func assertManifestBytes(t *testing.T, root *os.Root, path string, want []byte) {
	t.Helper()
	got, err := root.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("manifest %s: %q, %v; want %q", path, got, err, want)
	}
}
