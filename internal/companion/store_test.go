package companion

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectedOpenDoesNotEnrollStorage(t *testing.T) {
	for _, reason := range []string{"cancelled", "unknown store", "invalid parent"} {
		t.Run(reason, func(t *testing.T) {
			library := t.TempDir()
			ctx := t.Context()
			name := ColorsName
			switch reason {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "unknown store":
				name = "preferences.db"
			case "invalid parent":
				if err := os.WriteFile(filepath.Join(library, ".engine-cli"), []byte("owned file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			d, created, err := OpenWrite(ctx, library, name)
			if err == nil || d != nil || created {
				t.Fatalf("rejected open returned database=%v created=%t error=%v", d, created, err)
			}
			if reason == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			entries, err := os.ReadDir(library)
			if err != nil {
				t.Fatal(err)
			}
			if reason == "invalid parent" {
				data, err := os.ReadFile(filepath.Join(library, ".engine-cli"))
				if err != nil || string(data) != "owned file" || len(entries) != 1 {
					t.Fatalf("invalid parent changed: %q %v entries=%v", data, err, entries)
				}
			} else if len(entries) != 0 {
				t.Fatalf("rejected open enrolled storage: %v", entries)
			}
		})
	}
}

func TestDamagedExistingStoreIsPreservedOnOpenFailure(t *testing.T) {
	library := t.TempDir()
	path := ColorsPath(library)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	want := []byte("not a SQLite database")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	d, created, err := OpenWrite(t.Context(), library, ColorsName)
	if err == nil || d != nil || created {
		t.Fatalf("damaged store accepted: %v %t %v", d, created, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("failed open discarded existing data: %q %v", got, err)
	}
}

func TestRemoveReportsBlockedStoreAndStillRemovesOwnedSidecars(t *testing.T) {
	path := ColorsPath(t.TempDir())
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "preserve")
	if err := os.WriteFile(marker, []byte("owned data"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.WriteFile(path+suffix, []byte("owned sidecar"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(path); err == nil {
		t.Fatal("removal hid the blocked database path")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "owned data" {
		t.Fatalf("removal discarded blocked store: %q %v", data, err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("owned sidecar %s retained: %v", suffix, err)
		}
	}
}
