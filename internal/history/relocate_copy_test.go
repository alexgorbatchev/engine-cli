package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"zombiezen.com/go/sqlite"
)

func TestUndoRelocationCopyRetainsHistoryOwner(t *testing.T) {
	from, to, restoreParent := copyRelocationFixture(t)
	second := filepath.Join(t.TempDir(), "second")
	entry, err := Run(t.Context(), from, "library.move", ScopeCollection, nil, func(ctx context.Context) error {
		if err := RelocateLibrary(ctx, from, to); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(from, ".engine-cli", "history", "lock.db")); err != nil {
			return fmt.Errorf("fixture did not retain the source owner through copy: %w", err)
		}
		if _, err := List(to); sqlite.ErrCode(err) != sqlite.ResultBusy {
			return fmt.Errorf("copied destination was not exclusively owned during publication: %v", err)
		}
		if err := RelocateLibrary(ctx, to, second); err == nil {
			return fmt.Errorf("second relocation under the same journal was accepted")
		}
		if _, err := os.Stat(second); !os.IsNotExist(err) {
			return fmt.Errorf("refused second relocation created a destination: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restoreParent()
	if runtime.GOOS != "windows" {
		// Restore permission to remove the empty source remnant; no contents
		// may remain after the copy has been verified and its owners released.
		if err := os.Remove(from); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(to)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID || entries[0].State != "complete" {
		t.Fatalf("copied history unavailable after publication: %v %v", entries, err)
	}
	if _, err := Undo(t.Context(), to, entry.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	entries, err = List(from)
	if err != nil || len(entries) != 2 || entries[0].UndoOf != entry.ID {
		t.Fatalf("restored history unavailable after Undo: %v %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(from, "song.mp3")); err != nil || string(data) != "original audio" {
		t.Fatalf("relocation and Undo changed media: %q %v", data, err)
	}
}

func TestUndoRelocationCopyFailureReleasesDestination(t *testing.T) {
	from, to, _ := copyRelocationFixture(t)
	broken := filepath.Join(from, "broken.db")
	if err := os.WriteFile(broken, []byte("invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(broken)
	if err != nil {
		t.Fatal(err)
	}
	// Files-only history lets the native database copy encounter the invalid
	// input after acquiring the destination owner, before consuming any source.
	if _, err := Run(t.Context(), from, "library.move", ScopeFiles, nil, func(ctx context.Context) error {
		return RelocateLibrary(ctx, from, to)
	}); err == nil {
		t.Fatal("invalid database was copied")
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("failed copy retained destination or its open owner: %v", err)
	}
	if err := os.Mkdir(to, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := lock(t.Context(), filepath.Join(to, ".engine-cli", "history"))
	if err != nil {
		t.Fatalf("cannot reacquire failed copy destination: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(broken)
	if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("failed copy changed source metadata: %v", err)
	}
	if data, err := os.ReadFile(broken); err != nil || string(data) != "invalid database" {
		t.Fatalf("failed copy changed source bytes: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(from, "song.mp3")); err != nil || string(data) != "original audio" {
		t.Fatalf("failed copy consumed media: %q %v", data, err)
	}
	entries, err := List(from)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unchanged failed copy retained history or source ownership: %v %v", entries, err)
	}
}

func copyRelocationFixture(t *testing.T) (from, to string, restoreParent func()) {
	t.Helper()
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("copy fallback fixture requires enforced directory permissions")
	}
	source, database, _ := fixture(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	from = filepath.Join(parent, "library")
	if err := os.Rename(source, from); err != nil {
		t.Fatal(err)
	}
	to = filepath.Join(t.TempDir(), "moved")
	restoreParent = func() {
		if runtime.GOOS != "windows" {
			if err := os.Chmod(parent, 0700); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(restoreParent)
	// POSIX permits copying children but refuses renaming this directory from
	// its unwritable parent. Windows already refuses moving the open lock.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(parent, 0500); err != nil {
			t.Fatal(err)
		}
	}
	return from, to, restoreParent
}
