package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryEntryPublicationInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	for _, path := range []string{source, destination} {
		if err := os.WriteFile(path, []byte("retained"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, paths := range [][2]string{{source + "\x00", destination}, {source, destination + "\x00"}} {
		if err := publishEntry(paths[0], paths[1]); err == nil {
			t.Fatal("accepted an unrepresentable native publication path")
		}
		for _, path := range []string{source, destination} {
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "retained" {
				t.Fatalf("invalid path changed %s: %q, %v", path, got, err)
			}
		}
	}
}
