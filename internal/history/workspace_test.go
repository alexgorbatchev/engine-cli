package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedWorkspaceOnlyOperationsLeaveNoHistory(t *testing.T) {
	failure := errors.New("tool failed")
	tests := []struct {
		name     string
		failure  error
		run      func(ctx context.Context, d *sql.DB, workspace, song string) error
		recorded bool
	}{
		{
			name:    "failure confined to workspace",
			failure: failure,
			run: func(ctx context.Context, _ *sql.DB, workspace, _ string) error {
				return writeWorkspaceFile(ctx, workspace, "manifest.json", "failed")
			},
		},
		{
			name:    "failure rewrites existing workspace file",
			failure: failure,
			run: func(ctx context.Context, _ *sql.DB, workspace, _ string) error {
				return writeWorkspaceFile(ctx, workspace, "existing.json", "retried")
			},
		},
		{
			name:    "failure after a database change",
			failure: failure,
			run: func(ctx context.Context, d *sql.DB, workspace, _ string) error {
				if err := writeWorkspaceFile(ctx, workspace, "manifest.json", "failed"); err != nil {
					return err
				}
				_, err := d.Exec("UPDATE Track SET title='Partial'")
				return err
			},
			recorded: true,
		},
		{
			name:    "failure after a file change outside the workspace",
			failure: failure,
			run: func(ctx context.Context, _ *sql.DB, workspace, song string) error {
				if err := writeWorkspaceFile(ctx, workspace, "manifest.json", "failed"); err != nil {
					return err
				}
				if err := Preserve(ctx, song); err != nil {
					return err
				}
				return os.Remove(song)
			},
			recorded: true,
		},
		{
			name: "success confined to workspace",
			run: func(ctx context.Context, _ *sql.DB, workspace, _ string) error {
				return writeWorkspaceFile(ctx, workspace, "manifest.json", "ready")
			},
			recorded: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib, d, song := fixture(t)
			ctx := context.Background()
			workspace := filepath.Join(lib, ".engine-cli", "staging", "item")
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "existing.json"), []byte("failed"), 0o600); err != nil {
				t.Fatal(err)
			}
			e, err := Run(ctx, lib, "track.fetch", nil, func(ctx context.Context) error {
				if err := Workspace(ctx, workspace); err != nil {
					return err
				}
				return errors.Join(tt.run(ctx, d, workspace, song), tt.failure)
			})
			if tt.failure != nil && !errors.Is(err, tt.failure) {
				t.Fatalf("lost operation failure: %v", err)
			}
			if tt.failure == nil && err != nil {
				t.Fatal(err)
			}
			list, listErr := List(lib)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if tt.recorded {
				if e == nil || len(list) != 1 || list[0].ID != e.ID {
					t.Fatalf("entry=%+v history=%+v", e, list)
				}
				return
			}
			if e != nil || len(list) != 0 {
				t.Fatalf("failed workspace-only operation recorded entry=%+v history=%+v", e, list)
			}
			assertNoHistoryResidue(t, lib)
		})
	}
}

func TestDiscardedHistoryKeepsObjectsSharedWithOtherEntries(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	workspace := filepath.Join(lib, ".engine-cli", "staging", "item")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(workspace, "manifest.json")
	if err := os.WriteFile(manifest, []byte("failed"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(lib, "other.json")
	if err := os.WriteFile(other, []byte("failed"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := Run(ctx, lib, "edit", []string{other}, func(context.Context) error { return os.Remove(other) })
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("tool failed")
	if _, err = Run(ctx, lib, "track.fetch", nil, func(ctx context.Context) error {
		if err := Workspace(ctx, workspace); err != nil {
			return err
		}
		return errors.Join(writeWorkspaceFile(ctx, workspace, "manifest.json", "retried"), failure)
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err = Undo(ctx, lib, kept.ID, UndoOptions{}); err != nil {
		t.Fatalf("discarding a failure removed content another entry needs: %v", err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "failed" {
		t.Fatalf("restored %q err=%v", data, err)
	}
}

func TestWorkspaceOutsideHistoryIsIgnored(t *testing.T) {
	if err := Workspace(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Workspace(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func writeWorkspaceFile(ctx context.Context, workspace, name, content string) error {
	path := filepath.Join(workspace, name)
	if err := Preserve(ctx, path); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func assertNoHistoryResidue(t *testing.T, lib string) {
	t.Helper()
	dir := filepath.Join(lib, ".engine-cli", "history")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "lock.db" {
			continue
		}
		if entry.Name() != "objects" {
			t.Fatalf("history residue %s", entry.Name())
		}
		objects, err := os.ReadDir(filepath.Join(dir, "objects"))
		if err != nil {
			t.Fatal(err)
		}
		if len(objects) != 0 {
			t.Fatalf("orphaned history objects: %d", len(objects))
		}
	}
}
