package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
)

func TestUndoPreparationOwnsBackupBoundary(t *testing.T) {
	for _, mode := range []string{"apply", "dry-run", "pending", "backup-failure"} {
		t.Run(mode, func(t *testing.T) {
			lib, database, _ := fixture(t)
			entry, err := Run(t.Context(), lib, "track.update", ScopeCollection, nil, func(ctx context.Context) error {
				_, err := database.ExecContext(ctx, `UPDATE Track SET title='Changed' WHERE id=1`)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pending" {
				active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
				if err := os.Mkdir(active, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(active, "pending.json"), []byte("pending"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			backupErr := errors.New("backup failed")
			opts := UndoOptions{DryRun: mode == "dry-run", Prepare: func(ctx context.Context) error {
				called = true
				dir, err := storeDir(lib)
				if err != nil {
					return err
				}
				owner, err := lock(ctx, dir)
				if err == nil {
					return errors.Join(errors.New("publisher acquired history during Undo backup"), owner.Close())
				}
				if sqlite.ErrCode(err) != sqlite.ResultBusy {
					return err
				}
				if mode == "backup-failure" {
					return backupErr
				}
				return nil
			}}
			_, err = Undo(t.Context(), lib, entry.ID, opts)
			wantErr := mode == "pending" || mode == "backup-failure"
			if (err != nil) != wantErr {
				t.Fatalf("Undo error = %v", err)
			}
			if mode == "backup-failure" && !errors.Is(err, backupErr) {
				t.Fatalf("backup error lost: %v", err)
			}
			if called != (mode == "apply" || mode == "backup-failure") {
				t.Fatalf("backup called = %t", called)
			}
			var title string
			if err := database.QueryRow(`SELECT title FROM Track WHERE id=1`).Scan(&title); err != nil {
				t.Fatal(err)
			}
			want := "Changed"
			if mode == "apply" {
				want = "Original"
			}
			if title != want {
				t.Fatalf("title = %q, want %q", title, want)
			}
		})
	}
}
