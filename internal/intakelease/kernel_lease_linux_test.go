package intakelease

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"testing"

	"golang.org/x/sys/unix"
)

type kernelLeaseBarrier struct {
	child   *exec.Cmd
	input   io.WriteCloser
	scanner *bufio.Scanner
	joined  bool
}

func holdKernelLease(t *testing.T, ctx context.Context, path string) *kernelLeaseBarrier {
	t.Helper()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReservationKernelLeaseChild$")
	child.Env = append(os.Environ(), "ENGINE_INTAKE_KERNEL_LEASE_CHILD="+path)
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	b := &kernelLeaseBarrier{child: child, input: input, scanner: bufio.NewScanner(output)}
	t.Cleanup(func() {
		if !b.joined {
			if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			if err := child.Wait(); err == nil {
				t.Error("unjoined lease helper unexpectedly succeeded")
			}
		}
	})
	if !b.scanner.Scan() {
		t.Fatalf("kernel lease readiness: %v", b.scanner.Err())
	}
	if b.scanner.Text() == "unsupported" {
		if err := child.Wait(); err != nil {
			t.Fatal(err)
		}
		b.joined = true
		t.Skip("native filesystem does not support Linux file leases")
	}
	if b.scanner.Text() != "ready" {
		t.Fatalf("unexpected lease readiness: %q", b.scanner.Text())
	}
	return b
}

func (b *kernelLeaseBarrier) waitBlocked(t *testing.T) {
	t.Helper()
	if !b.scanner.Scan() || b.scanner.Text() != "blocked" {
		t.Fatalf("kernel did not confirm blocked acquisition: %q %v", b.scanner.Text(), b.scanner.Err())
	}
}

func (b *kernelLeaseBarrier) release(t *testing.T) {
	t.Helper()
	if err := b.input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.child.Wait(); err != nil {
		t.Fatal(err)
	}
	b.joined = true
}

func TestReservationKernelLeaseChild(t *testing.T) {
	path := os.Getenv("ENGINE_INTAKE_KERNEL_LEASE_CHILD")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	notifications := make(chan os.Signal, 1)
	signal.Notify(notifications, unix.SIGIO)
	defer signal.Stop(notifications)
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETOWN, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETLEASE, unix.F_WRLCK); err != nil {
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
			if _, err := fmt.Fprintln(os.Stdout, "unsupported"); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	<-notifications
	if _, err := fmt.Fprintln(os.Stdout, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETLEASE, unix.F_UNLCK); err != nil {
		t.Fatal(err)
	}
}
