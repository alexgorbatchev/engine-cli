//go:build !windows

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestCommandCancellationWaitsForDescendantCleanup(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "COVERAGE_HELPER_DEPTH=0", "COVERAGE_HELPER_DIR="+dir, "COVERAGE_HELPER_ADDRESS="+listener.Addr().String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cleanup also applies when a regression kills just the parent process.
	t.Cleanup(func() {
		for depth := range 3 {
			data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("pid-%d", depth)))
			if err == nil {
				pid, _ := strconv.Atoi(string(data))
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := commandOutput(ctx, env, executable, "-test.run=^TestCoverageProcessHelper$")
		done <- err
	}()
	conn, err := listener.Accept()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	conn.Close() // The grandchild signals that all three handlers are installed.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	for depth := range 3 {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("stopped-%d", depth))); err != nil {
			t.Errorf("descendant %d cleanup missing: %v", depth, err)
		}
	}
}

func TestCoverageProcessHelper(t *testing.T) {
	raw := os.Getenv("COVERAGE_HELPER_DEPTH")
	if raw == "" {
		return
	}
	depth, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("COVERAGE_HELPER_DIR")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("pid-%d", depth)), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	var child *exec.Cmd
	if depth < 2 {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		child = exec.Command(executable, "-test.run=^TestCoverageProcessHelper$")
		child.Env = append(os.Environ(), "COVERAGE_HELPER_DEPTH="+strconv.Itoa(depth+1))
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
	} else {
		conn, err := net.DialTimeout("tcp", os.Getenv("COVERAGE_HELPER_ADDRESS"), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	<-ctx.Done()
	if child != nil {
		if err := child.Wait(); err != nil {
			t.Error(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("stopped-%d", depth)), []byte("cleaned"), 0600); err != nil {
		t.Fatal(err)
	}
}
