package intake

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestWatchedSourceRefusesInvalidAuthorizationAndRecovers(t *testing.T) {
	root := t.TempDir()
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	root = dir.Path()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "source.wav")
	if err := os.WriteFile(path, []byte("retained original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, root, identity, path string }{
		{"missing-root", filepath.Join(root, "absent"), identity, path},
		{"wrong-identity", root, identity + "changed", path},
		{"root-itself", root, identity, root},
		{"outside-root", root, identity, filepath.Join(filepath.Dir(root), "outside.wav")},
		{"missing-ancestor", root, identity, filepath.Join(root, "missing", "source.wav")},
		{"missing-file", root, identity, path + "-absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := openWatchedSource(tc.root, tc.identity, tc.path)
			if err == nil || selected != nil {
				if selected != nil {
					if err := selected.Close(); err != nil {
						t.Error(err)
					}
				}
				t.Fatalf("invalid authorization retained source: %v %v", selected, err)
			}
		})
	}
	selected, err := openWatchedSource(root, identity, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := selected.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.Rename(path, path+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := selected.Check(); err == nil {
		t.Fatal("missing current name still authorized publication")
	}
	if err := os.WriteFile(path, []byte("unrelated replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	watchCode(t, selected.Check(), "intake_source_changed")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+"-retained", path); err != nil {
		t.Fatal(err)
	}
	if err := selected.Check(); err != nil {
		t.Fatal("restored identity refused:", err)
	}
	data, err := io.ReadAll(selected.file)
	if err != nil || string(data) != "retained original audio" {
		t.Fatalf("retained descriptor changed: %q %v", data, err)
	}
}
