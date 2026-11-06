package history

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func historyLeaseLibrary(t *testing.T) (string, string) {
	t.Helper()
	lib := t.TempDir()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	return lib, dir
}

func historyLeaseTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		value, err := historyLeaseTreeEntry(path, entry)
		if err != nil {
			return err
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func historyLeaseTreeDiff(before, after map[string]string) string {
	var changes []string
	for path, want := range before {
		if got, ok := after[path]; !ok || got != want {
			changes = append(changes, fmt.Sprintf("%s: before=%q after=%q present=%t", path, want, got, ok))
		}
	}
	for path, got := range after {
		if _, ok := before[path]; !ok {
			changes = append(changes, fmt.Sprintf("%s: added=%q", path, got))
		}
	}
	slices.Sort(changes)
	return "mode:size:mtime_ns:sha256\n" + strings.Join(changes, "\n")
}

func TestSmartlistProjectionSnapshotWithoutOperation(t *testing.T) {
	lib, _ := smartlistHistoryLibrary(t)
	before := historyLeaseTree(t, lib)
	after := historyLeaseTree(t, lib)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("source snapshot changed without a product operation:\n%s", historyLeaseTreeDiff(before, after))
	}
}

func TestSmartlistReadLeaseUsesReadOnlyStorage(t *testing.T) {
	lib, dir := historyLeaseLibrary(t)
	path := filepath.Join(dir, "lock.db")
	smartlistRestrict(t, path, 0400)
	smartlistRestrict(t, dir, 0500)
	smartlistRestrict(t, filepath.Dir(dir), 0500)
	before := historyLeaseTree(t, lib)
	got, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (string, error) {
		return ReadSmartlistSafe(ctx, lib, func(context.Context) (string, error) { return "nested read", nil })
	})
	if err != nil || got != "nested read" {
		t.Fatalf("read-only source refused: %q %v", got, err)
	}
	if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed history files, permissions, or modification times:\n%s", historyLeaseTreeDiff(before, after))
	}
}

func TestSmartlistReadLeaseRefusesUnsafeStorageWithoutWrites(t *testing.T) {
	for _, kind := range []string{"missing lock", "truncated lock", "unknown format", "rollback database", "WAL", "pending repair"} {
		t.Run(kind, func(t *testing.T) {
			lib, dir := historyLeaseLibrary(t)
			path := filepath.Join(dir, "lock.db")
			switch kind {
			case "missing lock":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "retained-evidence"), []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			case "truncated lock", "unknown format":
				data := []byte("SQLite format 3\x00\x10\x00\x03\x03")
				if kind == "truncated lock" {
					data = data[:10]
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "WAL", "rollback database":
				c, err := open(t.Context(), path, sqlite.OpenReadWrite)
				if err != nil {
					t.Fatal(err)
				}
				query := "CREATE TABLE lease_proof (id INTEGER)"
				if kind == "WAL" {
					query = "PRAGMA journal_mode=WAL"
				}
				err = sqlitex.ExecuteTransient(c, query, nil)
				if err := errors.Join(err, c.Close()); err != nil {
					t.Fatal(err)
				}
			case "pending repair":
				pending := filepath.Join(lib, ".engine-cli", "smartlist-repair", "pending.json")
				if err := os.MkdirAll(filepath.Dir(pending), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pending, []byte("retained pending evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := historyLeaseTree(t, lib)
			called := false
			_, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (bool, error) { called = true; return true, nil })
			if err == nil || called {
				t.Fatalf("unsafe storage reached callback: called=%t err=%v", called, err)
			}
			if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed history or recovery evidence:\n%s", historyLeaseTreeDiff(before, after))
			}
			if kind == "WAL" || kind == "rollback database" {
				if owner, err := lock(t.Context(), dir); err == nil {
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
					t.Fatal("publisher accepted initialized lock storage")
				}
				if err := ObserveEntries(t.Context(), lib, func(context.Context, []Entry) error { return nil }); err == nil {
					t.Fatal("receipt owner accepted initialized lock storage")
				}
				if after := historyLeaseTree(t, lib); !reflect.DeepEqual(before, after) {
					t.Fatalf("exclusive-owner refusal created WAL coordination files:\n%s", historyLeaseTreeDiff(before, after))
				}
			}
		})
	}
}
