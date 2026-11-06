package intake

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
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const watchServiceChildLibrary = "ENGINE_WATCH_SERVICE_CHILD_LIBRARY"

func TestWatchServiceNativeEventsPrepareNewDescendant(t *testing.T) {
	store, library := queueFixture(t)
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	state, err := service.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if scan, err := service.WatchReconcile(t.Context()); err != nil || scan.Observed != 0 {
		t.Fatalf("empty registered root: %+v %v", scan, err)
	}
	// The explicit round has completed registration and set the periodic timer
	// to thirty seconds. This shorter deadline requires a real native event.
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	directory := filepath.Join(state.Roots[0].Path, "new-directory")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(writeImportWAV(t))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "new.wav")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	waitWatchObservation(t, ctx, func() (bool, error) {
		items, err := store.List(ctx)
		if err != nil || len(items) != 1 {
			return false, err
		}
		if items[0].Status == "failed" {
			return false, fmt.Errorf("native event preparation failed: %s", items[0].ErrorCode)
		}
		return items[0].Status == "staged" && items[0].SourcePath == source && items[0].HistoryID == "", nil
	})
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("native discovery changed original bytes: %v", err)
	}
}

func TestWatchServiceLifetimeChild(t *testing.T) {
	library := os.Getenv(watchServiceChildLibrary)
	if library == "" {
		return
	}
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.WatchReconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "watch-service-owned"); err != nil {
		t.Fatal(err)
	}
	var stop [1]byte
	_, _ = os.Stdin.Read(stop[:])
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchServiceNativeLifetimeRelease(t *testing.T) {
	for _, killed := range []bool{false, true} {
		name := "close"
		if killed {
			name = "process-death"
		}
		t.Run(name, func(t *testing.T) {
			store, library := queueFixture(t)
			state, err := store.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWatchServiceLifetimeChild$")
			command.Env = append(os.Environ(), watchServiceChildLibrary+"="+library)
			command.Stderr = os.Stderr
			out, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			in, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Error(err)
					}
					_ = command.Wait()
				}
			}()
			line := bufio.NewScanner(out)
			if !line.Scan() || line.Text() != "watch-service-owned" {
				t.Fatalf("native child readiness: %q %v", line.Text(), line.Err())
			}
			service, err := OpenService(t.Context(), library, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			opts := WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}
			_, err = service.WatchSetEnabled(ctx, opts, false)
			watchCode(t, err, "watch_owned")
			if killed {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := in.Close(); err != nil {
				t.Fatal(err)
			}
			err = command.Wait()
			waited = true
			if killed && err == nil || !killed && err != nil {
				t.Fatalf("native child terminal: %v", err)
			}
			after, err := service.WatchSetEnabled(ctx, opts, false)
			if err != nil || after.Revision != state.Revision+1 || after.Roots[0].Enabled {
				t.Fatalf("native process ownership did not release: %+v %v", after, err)
			}
		})
	}
}

func TestWatchServiceCloseCancelsAcceptedMutationWaitingForState(t *testing.T) {
	store, library := queueFixture(t)
	service, err := OpenService(t.Context(), library, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	err = intakestate.With(ctx, library, func(ctx context.Context) error {
		mutation := make(chan error, 1)
		go func() {
			_, err := service.WatchAdd(ctx, WatchAddOptions{Path: root})
			mutation <- err
		}()
		// The retained discovery transaction proves the command was accepted;
		// this test still owns the state gate, so config cannot have committed.
		waitWatchObservation(t, ctx, func() (bool, error) {
			owner, err := store.openOwner(ctx, "watch-owner.db", "watch_owned")
			if err != nil {
				return progress.ErrorCode(err) == "watch_owned", nil
			}
			return false, owner.Close()
		})
		closed := make(chan error, 1)
		go func() { closed <- service.Close() }()
		select {
		case err := <-mutation:
			if !errors.Is(err, context.Canceled) {
				return fmt.Errorf("accepted blocked mutation returned %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-closed:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.WatchList(t.Context())
	if err != nil || state.Revision != 0 || len(state.Roots) != 0 {
		t.Fatalf("cancelled accepted mutation published config: %+v %v", state, err)
	}
	owner, err := store.openOwner(t.Context(), "watch-owner.db", "watch_owned")
	if err != nil {
		t.Fatal("joined service retained native discovery ownership:", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
