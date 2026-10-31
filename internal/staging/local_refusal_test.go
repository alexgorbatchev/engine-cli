package staging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCopyRefusalRetainsSourceAndReservation(t *testing.T) {
	for _, name := range []string{"invalid reservation", "missing source", "source directory", "corrupt manifest", "missing ready audio"} {
		t.Run(name, func(t *testing.T) {
			store := New(t.TempDir())
			source := localSourceFixture(t)
			original, err := os.ReadFile(source.Path)
			if err != nil {
				t.Fatal(err)
			}
			id := "0123456789abcdef0123456789abcdef"
			var manifest []byte
			manifestPath := filepath.Join(store.root(), id, manifestName)
			switch name {
			case "invalid reservation":
				id = "../outside"
			case "missing source":
				if err := os.Rename(source.Path, source.Path+".retained"); err != nil {
					t.Fatal(err)
				}
			case "source directory":
				source.Path = t.TempDir()
			case "corrupt manifest", "missing ready audio":
				copied, err := store.CopyLocal(t.Context(), id, source)
				if err != nil {
					t.Fatal(err)
				}
				if name == "corrupt manifest" {
					if err := os.WriteFile(manifestPath, []byte("{invalid retained manifest"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(filepath.Join(store.root(), filepath.FromSlash(copied.Item.AudioPath))); err != nil {
					t.Fatal(err)
				}
				manifest, err = os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			if result, err := store.CopyLocal(t.Context(), id, source); err == nil || result != nil {
				t.Fatalf("unsafe copy resumed: %+v, %v", result, err)
			}
			if manifest != nil {
				after, err := os.ReadFile(manifestPath)
				if err != nil || !bytes.Equal(manifest, after) {
					t.Fatalf("refusal rewrote reservation evidence: %v", err)
				}
			}
			if name != "source directory" {
				path := source.Path
				if name == "missing source" {
					path += ".retained"
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(original, after) {
					t.Fatalf("refusal altered source bytes: %v", err)
				}
			}
		})
	}
}
