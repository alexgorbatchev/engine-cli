//go:build linux || darwin

package watchclock

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestAuthorityNativePermissionFailuresRecover(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("native permission refusal requires an unprivileged process")
	}
	library := t.TempDir()
	err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
		dir := filepath.Join(library, ".engine-cli")
		var authority string
		defer func() {
			// Restore search permission before touching a child. This also
			// runs when a refusal assertion terminates the callback early.
			if err := os.Chmod(dir, 0700); err != nil {
				t.Error("restore authority parent permissions:", err)
			}
			if authority != "" {
				if err := os.Chmod(authority, 0600); err != nil {
					t.Error("restore authority file permissions:", err)
				}
			}
		}()
		if err := os.Chmod(dir, 0500); err != nil {
			return err
		}
		if _, err := Advance(ctx, library, 0); err == nil {
			t.Fatal("read-only parent permitted first authority publication")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
		if _, err := Advance(ctx, library, 0); err != nil {
			return err
		}
		path := filepath.Join(dir, filename)
		authority = path
		if err := os.Chmod(path, 0000); err != nil {
			return err
		}
		if _, err := Read(ctx, library); err == nil {
			t.Fatal("unreadable authority accepted")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0000); err != nil {
			return err
		}
		if _, err := Read(ctx, library); err == nil {
			t.Fatal("unsearchable authority parent accepted")
		}
		if _, err := Advance(ctx, library, 0); err == nil {
			t.Fatal("unsearchable authority parent allocated")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
		state, err := Read(ctx, library)
		if err == nil && state != (State{Revision: 1}) {
			t.Fatalf("native refusals changed authority: %+v", state)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
