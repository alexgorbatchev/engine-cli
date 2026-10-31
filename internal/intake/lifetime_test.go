package intake

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestStoreLifetimeChild(t *testing.T) {
	library := os.Getenv("ENGINE_INTAKE_STORE_LIFETIME_CHILD")
	if library == "" {
		return
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := fmt.Fprintln(os.Stdout, "store-ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLifetimeExcludesReplacementAcrossProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	library := t.TempDir()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreLifetimeChild$")
	child.Env = append(os.Environ(), "ENGINE_INTAKE_STORE_LIFETIME_CHILD="+library)
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			if err := child.Wait(); err == nil {
				t.Error("killed child unexpectedly succeeded")
			}
		}
	})
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "store-ready" {
		t.Fatalf("native Store readiness = %q, %v", scanner.Text(), scanner.Err())
	}
	if lease, err := intakelease.Replace(ctx, library); progress.ErrorCode(err) != "intake_replacing" {
		if lease != nil {
			defer lease.Close()
		}
		t.Fatalf("replacement bypassed a live queue Store: %v", err)
	}
	// Normal Store.Close, rather than killing the SQLite owner, must release
	// the observation only after its queue connections have closed.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	lease, err := intakelease.Replace(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := Open(ctx, library); progress.ErrorCode(err) != "intake_replacing" || store != nil {
		if store != nil {
			defer store.Close()
		}
		t.Fatalf("Store opened during exclusive replacement: %v %v", store, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(ctx, sourceFixture(t, "after-replacement.wav")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
