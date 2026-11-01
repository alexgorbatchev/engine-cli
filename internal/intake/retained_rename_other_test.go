//go:build !windows

package intake

import "testing"

func retainedRenameWasDenied(t *testing.T, err error, _ bool) bool {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return false
}
