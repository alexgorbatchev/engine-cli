package history

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryEntryPublication(t *testing.T) {
	for _, name := range []string{"ordinary", "日本語", filepath.Join(strings.Repeat("a", 120), strings.Repeat("b", 120))} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "entry.json")
			for _, data := range [][]byte{[]byte(`{"state":"pending"}`), []byte(`{"state":"complete","operation":"track.edit"}`)} {
				if err := writeAtomic(path, data); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("published %q, error %v; want %q", got, err, data)
				}
				files, err := os.ReadDir(dir)
				if err != nil || len(files) != 1 || files[0].Name() != "entry.json" {
					t.Fatalf("publication left temporary entries: %v, %v", files, err)
				}
			}
		})
	}
}

func TestHistoryEntryPublicationPreservesConflicts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "keep")
	if err := os.WriteFile(child, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("replacement")); err == nil {
		t.Fatal("replaced a directory with a history entry")
	}
	got, err := os.ReadFile(child)
	if err != nil || string(got) != "retained" {
		t.Fatalf("conflict damaged: %q, %v", got, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "entry.json" {
		t.Fatalf("failed publication left temporary entries: %v, %v", files, err)
	}
	if err := writeAtomic(filepath.Join(dir, "missing", "entry.json"), nil); err == nil {
		t.Fatal("accepted a missing history directory")
	}
}
