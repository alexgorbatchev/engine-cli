package intakestate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const childLibrary = "ENGINE_INTAKE_STATE_CHILD"
const ownerReady = "state owner held\n"

func TestNativeOwnerChild(t *testing.T) {
	library := os.Getenv(childLibrary)
	if library == "" {
		return
	}
	if err := With(context.Background(), library, func(context.Context) error {
		if _, err := os.Stdout.WriteString(ownerReady); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, os.Stdin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOwnerProcessDeathReleases(t *testing.T) {
	library := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestNativeOwnerChild$")
	command.Env = append(os.Environ(), childLibrary+"="+library)
	in, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	ready := make(chan error, 1)
	go func() {
		buffer := make([]byte, len(ownerReady))
		_, err := io.ReadFull(out, buffer)
		if err == nil && string(buffer) != ownerReady {
			err = errors.New("unexpected owner readiness receipt")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child never acquired native ownership")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	if err := With(ctx, library, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("foreign native owner did not exclude callback: called=%v err=%v", called, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed native owner reported success")
	}
	joined = true
	if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
		t.Fatal("dead process retained ownership:", err)
	}
}

func TestNativeOwnerRefusesUnsafeStorage(t *testing.T) {
	for _, kind := range []string{"directory-file", "owner-directory", "directory-link", "owner-link"} {
		t.Run(kind, func(t *testing.T) {
			library := t.TempDir()
			dir := filepath.Join(library, ".engine-cli")
			outside := t.TempDir()
			switch kind {
			case "directory-file":
				if err := os.WriteFile(dir, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, ownerName)
				if kind == "owner-directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(filepath.Join(outside, "must-not-create.db"), path); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			if err := With(context.Background(), library, func(context.Context) error { called = true; return nil }); err == nil || called {
				t.Fatalf("unsafe owner storage admitted: called=%v err=%v", called, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("outside owner storage changed: %v %v", entries, err)
			}
		})
	}
}

func TestNativeOwnerRetainsExclusionUntilCallbackReturns(t *testing.T) {
	library := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- With(ctx, library, func(ctx context.Context) error {
			close(entered)
			<-release
			return ctx.Err()
		})
	}()
	<-entered
	cancel()
	wait, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	called := false
	err := With(wait, library, func(context.Context) error { called = true; return nil })
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("cancellation released callback ownership: called=%v err=%v", called, err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner callback cancellation: %v", err)
	}
	want := errors.New("durable callback refusal")
	if err := With(context.Background(), library, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatal("callback error lost:", err)
	}
	if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
		t.Fatal("callback refusal leaked native owner:", err)
	}
}

func TestNativeOwnerCancelledPreflightDoesNotEnroll(t *testing.T) {
	library := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := With(ctx, library, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled preflight: called=%v err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled preflight enrolled storage: %v", err)
	}
}

func TestNativeOwnerUnavailableStorageRefusesAndRecovers(t *testing.T) {
	for _, kind := range []string{"missing-library", "damaged-owner"} {
		t.Run(kind, func(t *testing.T) {
			library := filepath.Join(t.TempDir(), "library")
			if kind == "damaged-owner" {
				dir := filepath.Join(library, ".engine-cli")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ownerName), []byte("invalid sqlite owner"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			if err := With(context.Background(), library, func(context.Context) error { called = true; return nil }); err == nil || called {
				t.Fatalf("unavailable native owner admitted: called=%v err=%v", called, err)
			}
			if kind == "damaged-owner" {
				if err := os.Remove(filepath.Join(library, ".engine-cli", ownerName)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(library, 0700); err != nil {
				t.Fatal(err)
			}
			if err := With(context.Background(), library, func(context.Context) error { return nil }); err != nil {
				t.Fatal("recovered storage remained excluded:", err)
			}
		})
	}
}
