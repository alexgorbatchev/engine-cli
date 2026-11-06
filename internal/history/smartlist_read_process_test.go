package history

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
)

func TestSmartlistReadLeaseExcludesOtherProcess(t *testing.T) {
	for _, kind := range []string{"reader", "publisher"} {
		t.Run(kind, func(t *testing.T) {
			lib, dir := historyLeaseLibrary(t)
			release := holdHistoryLeaseProcess(t, lib, kind)
			if kind == "reader" {
				if _, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (bool, error) { return true, nil }); err != nil {
					t.Fatalf("independent readers did not share native lease: %v", err)
				}
				owner, err := lock(t.Context(), dir)
				if err == nil {
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
					t.Fatal("publisher passed another process's read lease")
				}
				if sqlite.ErrCode(err) != sqlite.ResultBusy {
					t.Fatalf("publisher refused for wrong reason: %v", err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			var err error
			if kind == "reader" {
				err = ObserveEntries(ctx, lib, func(context.Context, []Entry) error { return errors.New("receipt owner passed read lease") })
			} else {
				_, err = ReadSmartlistSafe(ctx, lib, func(context.Context) (bool, error) { return false, errors.New("reader passed publisher") })
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("native wait did not retain exclusion through cancellation: %v", err)
			}
			release()
			owner, err := lock(t.Context(), dir)
			if err != nil {
				t.Fatalf("child leaked native ownership: %v", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSmartlistReadLeaseSurvivesAnotherLocalOpen(t *testing.T) {
	lib, dir := historyLeaseLibrary(t)
	_, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (bool, error) {
		connection, err := openHistoryLock(t.Context(), dir, sqlite.OpenReadOnly, false)
		if err != nil {
			return false, err
		}
		if err := connection.Close(); err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHistoryLeaseChild$")
		cmd.Env = append(os.Environ(), "ENGINE_HISTORY_LEASE_CHILD="+lib, "ENGINE_HISTORY_LEASE_KIND=probe")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("independent publisher passed local lease: %w\n%s", err, output)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func holdHistoryLeaseProcess(t *testing.T, lib, kind string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHistoryLeaseChild$")
	cmd.Env = append(os.Environ(), "ENGINE_HISTORY_LEASE_CHILD="+lib, "ENGINE_HISTORY_LEASE_KIND="+kind)
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			// EOF lets the child close SQLite before the bounded context can kill it.
			if err := input.Close(); err != nil {
				t.Error(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Error(err)
			}
			cancel()
		})
	}
	t.Cleanup(release)
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "owned\n" {
		t.Fatalf("child failed to acquire lease: %q %v", line, err)
	}
	return release
}

func TestHistoryLeaseChild(t *testing.T) {
	lib := os.Getenv("ENGINE_HISTORY_LEASE_CHILD")
	if lib == "" {
		return
	}
	hold := func(context.Context) error {
		if _, err := fmt.Fprintln(os.Stdout, "owned"); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, os.Stdin)
		return err
	}
	if os.Getenv("ENGINE_HISTORY_LEASE_KIND") == "reader" {
		_, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (bool, error) { return true, hold(ctx) })
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(t.Context(), dir)
	if os.Getenv("ENGINE_HISTORY_LEASE_KIND") == "probe" {
		if err == nil {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
			t.Fatal("publisher acquired ownership through another process's read lease")
		}
		if sqlite.ErrCode(err) != sqlite.ResultBusy {
			t.Fatalf("publisher refused for wrong reason: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(hold(t.Context()), owner.Close()); err != nil {
		t.Fatal(err)
	}
}
