package diskspace

import (
	"path/filepath"
	"testing"
)

func TestAvailableReadsTheVolumeAndRefusesMissingPaths(t *testing.T) {
	dir := t.TempDir()
	if free, err := Available(dir); err != nil || free == 0 {
		t.Fatalf("free=%d err=%v", free, err)
	}
	if _, err := Available(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted missing volume")
	}
}
