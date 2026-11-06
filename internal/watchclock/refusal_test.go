package watchclock

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestAuthorityRefusesInvalidInputWithoutEnrollment(t *testing.T) {
	library := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Read(ctx, library); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled observation:", err)
	}
	if _, err := Advance(t.Context(), library, -1); err == nil {
		t.Fatal("negative generation accepted")
	}
	if _, err := os.Lstat(filepath.Join(library, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusal enrolled native storage:", err)
	}
}

func TestAuthorityRefusesNativeStorageObstructions(t *testing.T) {
	for _, obstruction := range []string{"operational directory is a file", "authority is a directory"} {
		t.Run(obstruction, func(t *testing.T) {
			library := t.TempDir()
			dir := filepath.Join(library, ".engine-cli")
			path := dir
			if obstruction == "authority is a directory" {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(dir, filename)
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("unrelated native file"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Read(t.Context(), library); err == nil {
				t.Fatal("observation accepted native obstruction")
			}
			if _, err := Advance(t.Context(), library, 0); err == nil {
				t.Fatal("allocation accepted native obstruction")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("refusal replaced obstruction: %v", err)
			}
			if !before.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "unrelated native file" {
					t.Fatalf("refusal modified unrelated file: %q %v", data, err)
				}
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
				revision, err := Advance(ctx, library, 0)
				if err == nil && revision != 1 {
					t.Fatalf("recovered generation = %d", revision)
				}
				return err
			}); err != nil {
				t.Fatal("recovery:", err)
			}
		})
	}
}

func TestAuthorityFailedFirstAllocationDoesNotPublish(t *testing.T) {
	library := t.TempDir()
	err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
		if _, err := Restore(ctx, library, math.MaxInt64); err == nil {
			t.Fatal("exhausted first allocation accepted")
		}
		state, err := Read(ctx, library)
		if err != nil || state != (State{}) {
			t.Fatalf("failed initialization published authority: %+v %v", state, err)
		}
		private, err := filepath.Glob(filepath.Join(library, ".engine-cli", ".watch-authority-*.db*"))
		if err != nil || len(private) != 0 {
			t.Fatalf("failed initialization retained scratch: %v %v", private, err)
		}
		revision, err := Advance(ctx, library, 0)
		if err == nil && revision != 1 {
			t.Fatalf("failed publication consumed an unpublished generation: %d", revision)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityUpdateFailureRollsBackAndInvalidTupleRefuses(t *testing.T) {
	library := t.TempDir()
	err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
		if _, err := Restore(ctx, library, 10); err != nil {
			return err
		}
		path := filepath.Join(library, ".engine-cli", filename)
		d, err := db.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := d.Close(); err != nil {
				t.Error("close authority fixture:", err)
			}
		}()
		before, err := Read(ctx, library)
		if err != nil {
			return err
		}
		if _, err := d.ExecContext(ctx, `CREATE TRIGGER refuse_authority BEFORE UPDATE ON Authority BEGIN SELECT RAISE(ABORT,'fixture refuses publication'); END`); err != nil {
			return err
		}
		if _, err := Advance(ctx, library, 100); err == nil {
			t.Fatal("aborted update accepted")
		}
		after, err := Read(ctx, library)
		if err != nil || after != before {
			t.Fatalf("failed allocation changed retained fence: %+v %+v %v", before, after, err)
		}
		if _, err := d.ExecContext(ctx, "DROP TRIGGER refuse_authority"); err != nil {
			return err
		}
		revision, err := Advance(ctx, library, 0)
		if err != nil || revision != before.Revision+1 {
			t.Fatalf("rollback leaked transaction or consumed generation: %d %v", revision, err)
		}
		for _, damage := range []string{"UPDATE Authority SET restored=revision+1", "DELETE FROM Authority"} {
			if _, err := d.ExecContext(ctx, "PRAGMA ignore_check_constraints=ON;"+damage); err != nil {
				return err
			}
			if _, err := Read(ctx, library); err == nil {
				t.Fatal("damaged authority accepted:", damage)
			}
			if _, err := Advance(ctx, library, 0); err == nil {
				t.Fatal("damaged authority allocated:", damage)
			}
			if _, err := d.ExecContext(ctx, "DELETE FROM Authority; INSERT INTO Authority VALUES(1,?,?)", revision, before.Restored); err != nil {
				return err
			}
		}
		state, err := Read(ctx, library)
		if err == nil && state != (State{Revision: revision, Restored: before.Restored}) {
			t.Fatalf("repair did not retain exact fence: %+v", state)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
