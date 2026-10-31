//go:build linux || darwin

package fileidentity

import (
	"os"
	"testing"
)

func openReplaceable(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
