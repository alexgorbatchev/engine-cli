package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistPendingRepairBlocksHistory(t *testing.T) {
	lib, d, _ := fixture(t)
	ctx := context.Background()
	entry, err := Run(ctx, lib, "track.edit", ScopeCollection, nil, func(context.Context) error {
		_, err := d.Exec("UPDATE Track SET title='Changed'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.Mkdir(active, 0700); err != nil {
		t.Fatal(err)
	}
	// Even unreadable intent must block observation rather than being ignored.
	marker := filepath.Join(active, "pending.json")
	if err := os.WriteFile(marker, []byte("incomplete intent"), 0600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "objects", "retain-pending-proof")
	if err := os.MkdirAll(filepath.Dir(orphan), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("rollback evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list", func() error { _, err := List(lib); return err }},
		{"write", func() error {
			_, err := Run(ctx, lib, "blocked", ScopeCollection, nil, func(context.Context) error {
				t.Error("ordinary mutation ran over pending repair")
				return nil
			})
			return err
		}},
		{"undo", func() error { _, err := Undo(ctx, lib, entry.ID, UndoOptions{}); return err }},
		{"forced undo", func() error { _, err := Undo(ctx, lib, entry.ID, UndoOptions{Force: true}); return err }},
		{"preview", func() error { _, err := Undo(ctx, lib, entry.ID, UndoOptions{DryRun: true}); return err }},
		{"collect", func() error { return collect(dir, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); progress.ErrorCode(err) != "smartlist_repair_pending" {
				t.Fatalf("pending repair was not refused: %v", err)
			}
		})
	}
	if data, err := os.ReadFile(orphan); err != nil || string(data) != "rollback evidence" {
		t.Fatalf("pending evidence changed: %q %v", data, err)
	}
	var title string
	if err := d.QueryRow("SELECT title FROM Track").Scan(&title); err != nil || title != "Changed" {
		t.Fatalf("pending operation changed collection: %q %v", title, err)
	}
	if err := os.Rename(marker, filepath.Join(active, "committed.json")); err != nil {
		t.Fatal(err)
	}
	if entries, err := List(lib); err != nil || len(entries) != 1 {
		t.Fatalf("terminal repair blocked history: %+v %v", entries, err)
	}
}

func TestSmartlistRepairPreflight(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	if err := CheckSmartlistRepair(ctx, lib); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := CheckSmartlistRepair(cancelled, lib); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check: %v", err)
	}
	if err := CheckSmartlistRepair(ctx, filepath.Join(lib, "missing")); err == nil {
		t.Fatal("accepted absent library")
	}
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(active, 0700); err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []string{"committed.json", "rolled-back.json"} {
		if err := os.WriteFile(filepath.Join(active, terminal), []byte("retained terminal evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckSmartlistRepair(ctx, lib); err != nil {
		t.Fatalf("terminal evidence blocked preflight: %v", err)
	}
	// A dangling marker still denotes pending state, including alongside terminal
	// evidence. Never follow it and accidentally interpret ENOENT as completion.
	if err := os.Symlink(filepath.Join(lib, "absent"), filepath.Join(active, "pending.json")); err != nil {
		t.Fatal(err)
	}
	if err := CheckSmartlistRepair(ctx, lib); progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("dangling pending marker accepted: %v", err)
	}
	if err := os.RemoveAll(active); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("invalid repair storage"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckSmartlistRepair(ctx, lib); progress.ErrorCode(err) != "smartlist_repair_pending" {
		t.Fatalf("invalid repair storage accepted: %v", err)
	}
}

func TestSmartlistHistoryReadUsesPublisherLockWithoutEnrollment(t *testing.T) {
	lib := t.TempDir()
	if entries, err := List(lib); err != nil || len(entries) != 0 {
		t.Fatalf("empty history: %+v %v", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("empty history enrolled storage: %v", err)
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if entries, err := List(lib); err != nil || len(entries) != 0 {
		t.Fatalf("existing empty history: %+v %v", entries, err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("empty history created lock storage: %+v %v", files, err)
	}
	owner, err := lock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := List(lib); err == nil {
		t.Error("history read bypassed active publisher lock")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, err := List(lib); err != nil || len(entries) != 0 {
		t.Fatalf("released publisher still blocks: %+v %v", entries, err)
	}
}
