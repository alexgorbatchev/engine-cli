package intake

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func retainedRenameWasDenied(t *testing.T, err error, directory bool) bool {
	t.Helper()
	want := windows.ERROR_SHARING_VIOLATION
	if directory {
		// Windows refuses ancestor renames while descendant files are open,
		// even when the retained handles permit delete sharing.
		want = windows.ERROR_ACCESS_DENIED
	}
	if !errors.Is(err, want) {
		t.Fatalf("retained source rename: got %v, want %v", err, want)
	}
	return true
}
