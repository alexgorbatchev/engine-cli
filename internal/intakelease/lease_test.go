package intakelease

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestLeaseMissingPathDoesNotEnrollCurrentDirectory(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)
	for _, paths := range [][]string{nil, {""}, {filepath.Join(working, "valid"), ""}} {
		if lease, err := Replace(t.Context(), paths...); lease != nil || err == nil {
			t.Fatalf("invalid paths acquired owner: %v %v", lease, err)
		}
	}
	if lease, err := Observe(t.Context(), ""); lease != nil || err == nil {
		t.Fatalf("empty observation acquired owner: %v %v", lease, err)
	}
	files, err := os.ReadDir(working)
	if err != nil || len(files) != 0 {
		t.Fatalf("invalid paths created storage: %v %v", files, err)
	}
}

func TestLeaseChild(t *testing.T) {
	library := os.Getenv("ENGINE_INTAKE_LEASE_CHILD")
	if library == "" {
		return
	}
	lease, err := Observe(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := fmt.Fprintln(os.Stdout, "lease-ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseCrossProcessLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	library := t.TempDir()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLeaseChild$")
	child.Env = append(os.Environ(), "ENGINE_INTAKE_LEASE_CHILD="+library)
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
	if !scanner.Scan() || scanner.Text() != "lease-ready" {
		t.Fatalf("child did not acquire lifetime lease: %q %v", scanner.Text(), scanner.Err())
	}
	second, err := Observe(ctx, library)
	if err != nil {
		t.Fatalf("independent queue owners must overlap: %v", err)
	}
	if replacement, err := Replace(ctx, library); progress.ErrorCode(err) != "intake_replacing" {
		var closeErr error
		if replacement != nil {
			closeErr = replacement.Close()
		}
		t.Fatalf("replacement bypassed live child and second owner: %v %v", err, closeErr)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("killed child unexpectedly succeeded")
	}
	joined = true
	if replacement, err := Replace(ctx, library); err == nil {
		closeErr := replacement.Close()
		t.Fatalf("replacement bypassed remaining independent owner: %v", closeErr)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Replace(ctx, library)
	if err != nil {
		t.Fatalf("process death and final Close must release ownership: %v", err)
	}
	if observer, err := Observe(ctx, library); progress.ErrorCode(err) != "intake_replacing" {
		var closeErr error
		if observer != nil {
			closeErr = observer.Close()
		}
		t.Fatalf("queue opened during replacement without stable refusal: %v %v", err, closeErr)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	observer, err := Observe(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseRefusesUnsafeStorageAndCancelledAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	library := t.TempDir()
	if lease, err := Observe(ctx, library); !errors.Is(err, context.Canceled) || lease != nil {
		t.Fatalf("cancelled acquisition: %v %v", lease, err)
	}
	if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("cancelled acquisition enrolled storage: %v", err)
	}
	directory := filepath.Join(filepath.Dir(library), reservationDirectory)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, filepath.Base(library)), []byte("unexpected data"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, acquire := range []func(context.Context, string) (*Lease, error){Observe, func(ctx context.Context, path string) (*Lease, error) { return Replace(ctx, path) }} {
		if lease, err := acquire(t.Context(), library); err == nil || lease != nil {
			t.Fatalf("nonempty anchor cannot provide lifetime exclusion: %v %v", lease, err)
		}
	}
}

func TestLeaseOwnsBothMoveNamesUntilOutcome(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload"), []byte("library"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := Replace(t.Context(), destination, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, destination} {
		observer, err := Observe(t.Context(), path)
		if progress.ErrorCode(err) != "intake_replacing" || observer != nil {
			if observer != nil {
				defer observer.Close()
			}
			t.Fatalf("move released path %s before outcome: %v", path, err)
		}
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source absence contract violated: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 || entries[0].Name() != "payload" {
		t.Fatalf("coordination storage moved into library: %v %v", entries, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	observer, err := Observe(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}
