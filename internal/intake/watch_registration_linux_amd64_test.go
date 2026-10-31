package intake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const watchRegistrationChild = "ENGINE_WATCH_REGISTRATION_CHILD"

// Stop the real owner immediately before installing the descendant watch.
// Creating a file here cannot be covered by the preceding directory snapshot.
func TestWatchServicePreRegistrationDescendantIsPrepared(t *testing.T) {
	if library := os.Getenv(watchRegistrationChild); library != "" {
		watchRegistrationChildRun(t, library, os.Getenv("ENGINE_WATCH_REGISTRATION_ROOT"))
		return
	}
	_, library := queueFixture(t)
	root := t.TempDir()
	content, err := os.ReadFile(writeImportWAV(t))
	if err != nil {
		t.Fatal(err)
	}
	read, report, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(read.Close(), report.Close()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWatchServicePreRegistrationDescendantIsPrepared$", "-test.v")
	cmd.Env = append(os.Environ(), watchRegistrationChild+"="+library, "ENGINE_WATCH_REGISTRATION_ROOT="+root)
	cmd.ExtraFiles = []*os.File{report}
	output, err := os.CreateTemp(t.TempDir(), "owner-output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := output.Close(); err != nil {
			t.Error(err)
		}
	}()
	cmd.Stdout, cmd.Stderr = output, output
	start, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := start.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			killErr := cmd.Process.Kill()
			if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				t.Error(killErr)
			}
			if err := cmd.Wait(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) && !errors.Is(err, unix.ECHILD) {
					t.Error(err)
				}
			}
		}
	}()
	if err := read.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var tid int
	if _, err := fmt.Fscanln(read, &tid); err != nil {
		t.Fatalf("owner thread: %v\n%s", err, watchRegistrationOutput(t, output.Name()))
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.PtraceAttach(tid); err != nil {
		t.Fatal(err)
	}
	attached := true
	defer func() {
		if attached {
			if err := unix.PtraceDetach(tid); err != nil && !errors.Is(err, unix.ESRCH) {
				t.Error(err)
			}
		}
	}()
	if _, err := waitWatchRegistrationStop(tid); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceSetOptions(tid, unix.PTRACE_O_TRACESYSGOOD); err != nil {
		t.Fatal(err)
	}
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	descendant := filepath.Join(root, "new-directory")
	signal := 0
	for {
		if err := unix.PtraceSyscall(tid, signal); err != nil {
			t.Fatal(err)
		}
		status, err := waitWatchRegistrationStop(tid)
		if err != nil {
			t.Fatalf("descendant watch barrier: %v\n%s", err, watchRegistrationOutput(t, output.Name()))
		}
		signal = 0
		if status.StopSignal() != unix.Signal(int(unix.SIGTRAP)|0x80) {
			signal = int(status.StopSignal())
			continue
		}
		var regs unix.PtraceRegs
		if err := unix.PtraceGetRegs(tid, &regs); err != nil {
			t.Fatal(err)
		}
		if regs.Orig_rax != unix.SYS_INOTIFY_ADD_WATCH || int64(regs.Rax) != -int64(unix.ENOSYS) {
			continue
		}
		path, err := watchRegistrationPath(tid, uintptr(regs.Rsi))
		if err != nil {
			t.Fatal(err)
		}
		if path == descendant {
			break
		}
	}
	if err := os.WriteFile(filepath.Join(descendant, "new.wav"), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceDetach(tid); err != nil {
		t.Fatal(err)
	}
	attached = false
	err = cmd.Wait()
	joined = true
	if err != nil {
		t.Fatalf("real owner missed pre-registration descendant: %v\n%s", err, watchRegistrationOutput(t, output.Name()))
	}
	after, err := os.ReadFile(filepath.Join(descendant, "new.wav"))
	if err != nil || !bytes.Equal(after, content) {
		t.Fatalf("native preparation changed original source: %v", err)
	}
}

func watchRegistrationOutput(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func watchRegistrationPath(tid int, address uintptr) (string, error) {
	var path []byte
	for len(path) < 4096 {
		var word [8]byte
		if _, err := unix.PtracePeekData(tid, address+uintptr(len(path)), word[:]); err != nil {
			return "", err
		}
		for _, value := range word {
			if value == 0 {
				return string(path), nil
			}
			path = append(path, value)
		}
	}
	return "", errors.New("native watch path was not terminated")
}

func watchRegistrationChildRun(t *testing.T, library, root string) {
	t.Helper()
	store, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Keep the actual discovery owner on the reported thread. Preparation and
	// public requests retain their normal separate goroutine ownership.
	service := &Service{store: store, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), wake: make(chan struct{}, 1), watch: newWatchManager()}
	go service.run(ctx, 0)
	service.Wake()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	report := os.NewFile(3, "watch-registration-thread")
	defer func() {
		if err := report.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := fmt.Fprintln(report, unix.Gettid()); err != nil {
		t.Fatal(err)
	}
	var start [1]byte
	if _, err := os.Stdin.Read(start[:]); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- watchRegistrationExercise(ctx, service, root)
		cancel()
	}()
	service.runWatch(ctx, 0)
	if err := <-result; err != nil {
		t.Error(err)
	}
	if err := service.Close(); err != nil {
		t.Error(err)
	}
}

func watchRegistrationExercise(ctx context.Context, service *Service, root string) error {
	if _, err := service.WatchAdd(ctx, WatchAddOptions{Path: root, Recursive: true}); err != nil {
		return err
	}
	if _, err := service.WatchReconcile(ctx); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(root, "new-directory"), 0700); err != nil {
		return err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var items []Item
		err := service.Use(ctx, func(store *Store) (err error) { items, err = store.List(ctx); return err })
		if err != nil {
			return err
		}
		if len(items) == 1 && items[0].Status == "staged" && items[0].SourcePath == filepath.Join(root, "new-directory", "new.wav") && items[0].HistoryID == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("registered descendant was not prepared before periodic reconciliation: %+v", items)
		case <-tick.C:
		}
	}
}

func waitWatchRegistrationStop(tid int) (unix.WaitStatus, error) {
	for {
		var status unix.WaitStatus
		_, err := unix.Wait4(tid, &status, unix.WALL, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return status, err
		}
		if !status.Stopped() {
			return status, fmt.Errorf("owner terminated before native registration barrier: %v", status)
		}
		return status, nil
	}
}

func TestWatchEventsFailedConstructionReleasesNativeDescriptors(t *testing.T) {
	manager := newWatchManager()
	root := t.TempDir()
	entry := watchEventDirectory(t, root)
	before := watchInotifyDescriptors(t)
	for _, test := range []struct {
		name      string
		wanted    map[string]string
		cancelled bool
	}{
		{"missing-directory", map[string]string{root: entry.identity, filepath.Join(root, "missing"): entry.identity}, false},
		{"cancelled", map[string]string{root: entry.identity}, true},
		{"identity-changed-after-add", map[string]string{root: entry.identity + "-stale"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if test.cancelled {
				cancel()
			}
			events, err := manager.newWatchEvents(ctx, test.wanted)
			cancel()
			if events != nil || err == nil {
				if events != nil {
					if err := events.close(); err != nil {
						t.Error(err)
					}
				}
				t.Fatal("failed candidate reported successful native ownership")
			}
			if after := watchInotifyDescriptors(t); after != before {
				t.Fatalf("failed candidate leaked native registrations: before=%d after=%d", before, after)
			}
		})
	}
	events, err := manager.newWatchEvents(t.Context(), map[string]string{root: entry.identity})
	if err != nil {
		t.Fatal("valid installation after refusal:", err)
	}
	if !watchRegistrationsPresent(events) {
		t.Error("recovery did not install complete native coverage")
	}
	if err := events.close(); err != nil {
		t.Fatal(err)
	}
	if after := watchInotifyDescriptors(t); after != before {
		t.Fatalf("recovered owner close leaked native registrations: before=%d after=%d", before, after)
	}
}

func watchInotifyDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue // ReadDir's own descriptor has already closed.
		}
		if err != nil {
			t.Fatal(err)
		}
		if target == "anon_inode:inotify" {
			count++
		}
	}
	return count
}
