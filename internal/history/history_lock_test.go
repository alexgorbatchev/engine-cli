package history

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryOwnersPreserveEmptyLockDatabase(t *testing.T) {
	lib, dir := historyLeaseLibrary(t)
	assertEmpty := func(t *testing.T) {
		t.Helper()
		info, err := os.Stat(filepath.Join(dir, "lock.db"))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Size() != 0 {
			t.Fatalf("owner initialized lock database: %s, %d bytes", info.Mode(), info.Size())
		}
	}
	assertEmpty(t)
	for _, wait := range []bool{false, true} {
		owner, err := lockHistory(t.Context(), dir, wait)
		if err != nil {
			t.Fatal(err)
		}
		assertEmpty(t)
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		assertEmpty(t)
	}
	if err := ObserveEntries(t.Context(), lib, func(context.Context, []Entry) error {
		assertEmpty(t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t)
	if _, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (bool, error) {
		assertEmpty(t)
		return ReadSmartlistSafe(ctx, lib, func(context.Context) (bool, error) {
			assertEmpty(t)
			return true, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t)
}
