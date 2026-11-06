package watchfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDescendantLinksRefuse(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{root, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "source.wav"), []byte("must not read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-directory")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "source.wav"), filepath.Join(root, "linked-file")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "explicit-root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	dir, err := Open(alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || dir.Path() != canonical {
		t.Fatalf("explicit canonical authorization %q: %v", dir.Path(), err)
	}
	if _, err := dir.OpenDirectory("linked-directory"); err == nil {
		t.Fatal("followed descendant directory link")
	}
	if file, err := dir.OpenFile("linked-file"); err == nil {
		_ = file.Close()
		t.Fatal("accepted linked source")
	}
}
