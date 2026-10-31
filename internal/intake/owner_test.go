package intake

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const workerChildLibrary = "ENGINE_INTAKE_OWNER_CHILD_LIBRARY"

func TestWorkerOwnerChild(t *testing.T) {
	library := os.Getenv(workerChildLibrary)
	if library == "" {
		return
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.withWorker(t.Context(), func(context.Context) error {
		if _, err := fmt.Fprintln(os.Stdout, "intake-owned"); err != nil {
			return err
		}
		select {}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkerOwnershipReleasedByProcessDeath(t *testing.T) {
	s, library := queueFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerOwnerChild$")
	cmd.Env = append(os.Environ(), workerChildLibrary+"="+library)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			// Wait releases the native process handle; only kill an unreaped child.
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			_ = cmd.Wait() // Every started child is reaped, including assertion failure paths.
		}
	}()
	line := bufio.NewScanner(out)
	if !line.Scan() || line.Text() != "intake-owned" {
		cancel()
		err := cmd.Wait()
		waited = true
		t.Fatalf("child did not acquire native owner: %q, scan=%v, wait=%v", line.Text(), line.Err(), err)
	}
	if err := s.withWorker(t.Context(), func(context.Context) error { return nil }); progress.ErrorCode(err) != "intake_worker_busy" {
		t.Errorf("second process bypassed owner: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("killed owner unexpectedly exited successfully")
	}
	if err := s.withWorker(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("dead process retained native owner: %v", err)
	}
}

func TestWorkerOwnershipDoesNotBlockQueueWrites(t *testing.T) {
	s, library := queueFixture(t)
	other, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = s.withWorker(ctx, func(ctx context.Context) error {
		cancel()
		entered := false
		err := other.withWorker(t.Context(), func(context.Context) error {
			entered = true
			return nil
		})
		if entered || progress.ErrorCode(err) != "intake_worker_busy" {
			t.Fatalf("cancelled but unjoined owner lost exclusion: entered=%v, err=%v", entered, err)
		}
		item, err := other.Enqueue(t.Context(), sourceFixture(t, "while-owned.wav"))
		if err != nil || item == nil {
			t.Fatalf("owner blocked independent queue write: %+v, %v", item, err)
		}
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("worker cancellation: %v", err)
	}
	entered := false
	err = other.withWorker(t.Context(), func(context.Context) error {
		entered = true
		return nil
	})
	if err != nil || !entered {
		t.Fatalf("joined owner retained lock: entered=%v, err=%v", entered, err)
	}
}
