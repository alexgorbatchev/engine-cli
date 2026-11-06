package watchclock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

const enrollmentChild = "ENGINE_WATCH_CLOCK_ENROLLMENT_CHILD"

func TestAuthorityEnrollmentChild(t *testing.T) {
	phase := os.Getenv(enrollmentChild)
	if phase == "" {
		return
	}
	library := os.Getenv("ENGINE_WATCH_CLOCK_LIBRARY")
	err := intakestate.With(t.Context(), library, func(ctx context.Context) error {
		f, err := newInitialization(filepath.Join(library, ".engine-cli", filename))
		if err != nil {
			return err
		}
		path := f.Name()
		if err := f.Close(); err != nil {
			return err
		}
		if phase == "before-commit" {
			d, tx, err := beginAllocation(ctx, path, true)
			if err != nil {
				return err
			}
			defer d.Close()
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, "UPDATE Authority SET revision=41,restored=41"); err != nil {
				return err
			}
		} else {
			if _, err := allocateFile(ctx, path, 40, true, true); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stdout, "authority initialization held", phase)
		_, err = bufio.NewReader(os.Stdin).ReadByte()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityEnrollmentSurvivesKilledInitializer(t *testing.T) {
	for _, phase := range []string{"before-commit", "after-commit"} {
		t.Run(phase, func(t *testing.T) {
			library := t.TempDir()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAuthorityEnrollmentChild$")
			cmd.Env = append(os.Environ(), enrollmentChild+"="+phase, "ENGINE_WATCH_CLOCK_LIBRARY="+library)
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			t.Cleanup(func() {
				if !joined {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			line, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || line != "authority initialization held "+phase+"\n" {
				t.Fatalf("initializer checkpoint: %q %v %s", line, err, diagnostic.String())
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("initializer was not killed")
			}
			joined = true
			path := filepath.Join(library, ".engine-cli", filename)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unpublished authority became visible:", err)
			}
			state, err := Read(t.Context(), library)
			if err != nil || state != (State{}) {
				t.Fatalf("scratch initialization leaked into observation: %+v %v", state, err)
			}
			err = intakestate.With(t.Context(), library, func(ctx context.Context) error {
				revision, err := Restore(ctx, library, 50)
				if err != nil || revision != 51 {
					return fmt.Errorf("recovered enrollment: %d %w", revision, err)
				}
				revision, err = Advance(ctx, library, 0)
				if err != nil || revision != 52 {
					return fmt.Errorf("published generation reused: %d %w", revision, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			state, err = Read(t.Context(), library)
			if err != nil || state != (State{Revision: 52, Restored: 51}) {
				t.Fatalf("reopen lost published generation: %+v %v", state, err)
			}
		})
	}
}

func TestAuthorityPublicationNeverOverwritesExistingStorage(t *testing.T) {
	for _, contents := range []string{"", "unrelated native data"} {
		t.Run(fmt.Sprintf("bytes-%d", len(contents)), func(t *testing.T) {
			library := t.TempDir()
			dir := filepath.Join(library, ".engine-cli")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, filename)
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := initialize(t.Context(), path, 20, false); err == nil {
				t.Fatal("initialization overwrote an existing authority")
			}
			if _, err := Advance(t.Context(), library, 0); err == nil {
				t.Fatal("existing unsupported authority was reset")
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != contents {
				t.Fatalf("existing storage changed: %q %v", actual, err)
			}
		})
	}
}
