package pcm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPCMHelperProcess(t *testing.T) {
	mode := os.Getenv("PCM_HELPER_MODE")
	if mode == "" {
		return
	}
	switch mode {
	case "output":
		fmt.Print("123456789")
		os.Exit(0)
	case "error":
		os.Exit(19)
	case "wait":
		if err := os.WriteFile(os.Getenv("PCM_HELPER_STARTED"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(20)
		}
		time.Sleep(time.Hour)
	case "launcher":
		program, err := os.Executable()
		if err != nil {
			os.Exit(21)
		}
		child := exec.Command(program, "-test.run=^TestPCMHelperProcess$")
		child.Env = append(os.Environ(), "PCM_HELPER_MODE=descendant")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(22)
		}
		time.Sleep(30 * time.Second)
	case "descendant":
		if err := os.WriteFile(os.Getenv("PCM_HELPER_STARTED"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(23)
		}
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func TestCancellationWithDescendantHoldingOutput(t *testing.T) {
	t.Setenv("PCM_HELPER_MODE", "launcher")
	started := filepath.Join(t.TempDir(), "descendant")
	t.Setenv("PCM_HELPER_STARTED", started)
	s := New()
	t.Cleanup(s.Close)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.program = program
	g := testGeneration(t, s, "library")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := s.run(ctx, g, []string{"-test.run=^TestPCMHelperProcess$"}, 1024); finished <- err }()
	waitForChild(t, started)
	pidBytes, err := os.ReadFile(started)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	descendant, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer descendant.Kill()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = descendant.Kill()
		awaitChild(t, finished)
		t.Fatal("cancelled decoder remained blocked on descendant-held stdout")
	}
}

func TestDirectChildOutputBudgetAndWait(t *testing.T) {
	for _, tt := range []struct {
		mode  string
		limit int
		want  error
	}{{"output", 9, nil}, {"output", 8, ErrTimeline}, {"error", 8, ErrDecode}} {
		t.Run(fmt.Sprintf("%s-%d", tt.mode, tt.limit), func(t *testing.T) {
			t.Setenv("PCM_HELPER_MODE", tt.mode)
			s := New()
			t.Cleanup(s.Close)
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			s.program = program
			g := testGeneration(t, s, "library")
			data, err := s.run(t.Context(), g, []string{"-test.run=^TestPCMHelperProcess$"}, tt.limit)
			if !errors.Is(err, tt.want) {
				t.Fatalf("data=%q error=%v", data, err)
			}
			if tt.want == nil && string(data) != "123456789" {
				t.Fatalf("data=%q", data)
			}
		})
	}
}

func TestDirectChildCancellation(t *testing.T) {
	t.Setenv("PCM_HELPER_MODE", "wait")
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("PCM_HELPER_STARTED", started)
	s := New()
	t.Cleanup(s.Close)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.program = program
	g := testGeneration(t, s, "library")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := s.run(ctx, g, []string{"-test.run=^TestPCMHelperProcess$"}, 1024); finished <- err }()
	waitForChild(t, started)
	cancel()
	awaitChild(t, finished)
}

func TestCancelledBeforeStartDoesNotReportMissingDecoder(t *testing.T) {
	t.Setenv("PCM_HELPER_MODE", "wait")
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("PCM_HELPER_STARTED", started)
	s := New()
	t.Cleanup(s.Close)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.program = program
	g := testGeneration(t, s, "library")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.run(ctx, g, []string{"-test.run=^TestPCMHelperProcess$"}, 1024)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled start misclassified: %v", err)
	}
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled request started a child: %v", err)
	}
}

func waitForChild(t *testing.T, started string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("child did not start")
		}
	}
}

func awaitChild(t *testing.T, finished <-chan error) {
	t.Helper()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct child was not killed and joined")
	}
}

func TestTeardownCancelsAndJoinsActiveChild(t *testing.T) {
	for _, action := range []string{"release", "close"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("PCM_HELPER_MODE", "wait")
			started := filepath.Join(t.TempDir(), "started")
			t.Setenv("PCM_HELPER_STARTED", started)
			s := New()
			t.Cleanup(s.Close)
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			s.program = program
			g := testGeneration(t, s, "library")
			ctx, _, finish, err := s.acquire(t.Context(), "library", g.metadata.ID)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := s.run(ctx, g, []string{"-test.run=^TestPCMHelperProcess$"}, 1024)
				finish(err)
				finished <- err
			}()
			waitForChild(t, started)
			switch action {
			case "release":
				if err := s.Release("library", g.metadata.ID); err != nil {
					t.Fatal(err)
				}
			case "close":
				s.Close()
			}
			awaitChild(t, finished)
			if _, err := g.file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("teardown retained file: %v", err)
			}
			s.mu.Lock()
			active, resident := s.active, len(s.items)
			s.mu.Unlock()
			if active != 0 || resident != 0 {
				t.Fatalf("teardown active=%d resident=%d", active, resident)
			}
		})
	}
}

func TestDecoderStartAndClosedFile(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	g := testGeneration(t, s, "library")
	s.program = filepath.Join(t.TempDir(), "absent")
	if _, err := s.run(t.Context(), g, nil, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing program: %v", err)
	}
	if err := g.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(t.Context(), g, nil, 1); !errors.Is(err, ErrChanged) {
		t.Fatalf("closed retained file: %v", err)
	}
}
