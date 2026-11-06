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
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const watchCopyChild = "ENGINE_WATCH_COPY_CHILD"

// The child keeps PrepareNext on one native thread. The parent observes the
// second source read, after one buffer has actually reached the staging file.
func TestWatchPreparationRevocationDuringNativeCopy(t *testing.T) {
	if library := os.Getenv(watchCopyChild); library != "" {
		watchCopyChildRun(t, library)
		return
	}
	s, library := queueFixture(t)
	root := t.TempDir()
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "source.wav")
	content := bytes.Repeat([]byte("native retained source\n"), 32768)
	if err := os.WriteFile(original, content, 0600); err != nil {
		t.Fatal(err)
	}
	item, err := s.Enqueue(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	alias := original + "-renamed.wav"
	if err := os.Rename(original, alias); err != nil {
		t.Fatal(err)
	}
	if scan, _, err := s.reconcileWatch(t.Context(), 0); err != nil || scan.Associated != 1 {
		t.Fatalf("alias association: %+v %v", scan, err)
	}
	ready, report, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer report.Close()
	childCtx, stopChild := context.WithTimeout(t.Context(), 45*time.Second)
	defer stopChild()
	cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestWatchPreparationRevocationDuringNativeCopy$", "-test.v")
	cmd.Env = append(os.Environ(), watchCopyChild+"="+library)
	cmd.ExtraFiles = []*os.File{report}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	start, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer start.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			_ = cmd.Process.Kill() // Kill and Wait own only this test child.
			_ = cmd.Wait()
		}
	}()
	if err := ready.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var tid int
	if _, err := fmt.Fscanln(ready, &tid); err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.PtraceAttach(tid); err != nil {
		t.Fatal(err)
	}
	attached := true
	defer func() {
		if attached {
			_ = unix.PtraceDetach(tid) // Primary failure is reported by the caller.
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := waitWatchCopyStop(ctx, tid); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceSetOptions(tid, unix.PTRACE_O_TRACESYSGOOD); err != nil {
		t.Fatal(err)
	}
	if _, err := start.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	reads, signal := 0, 0
	for reads < 2 {
		if err := unix.PtraceSyscall(tid, signal); err != nil {
			t.Fatal(err)
		}
		status, err := waitWatchCopyStop(ctx, tid)
		if err != nil {
			t.Fatalf("source read barrier: %v", err)
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
		// Linux amd64 reports -ENOSYS in Rax at syscall entry.
		if regs.Orig_rax != unix.SYS_READ || int64(regs.Rax) != -int64(unix.ENOSYS) {
			continue
		}
		path, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "fd", strconv.FormatUint(regs.Rdi, 10)))
		if err == nil && path == alias {
			reads++
		}
	}
	staged, err := filepath.Glob(filepath.Join(library, ".engine-cli", "staging", "*", "*", filepath.Base(original)))
	if err != nil {
		t.Fatal(err)
	}
	partial := false
	for _, path := range staged {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() < int64(len(content)) {
			copied, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			partial = bytes.Equal(copied, content[:len(copied)])
		}
	}
	if !partial {
		t.Fatal("second exact source read did not follow a partial staging write")
	}
	if _, err := s.WatchSetEnabled(ctx, WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceDetach(tid); err != nil {
		t.Fatal(err)
	}
	attached = false
	err = cmd.Wait()
	joined = true
	if err != nil {
		t.Fatalf("revoked native copy child: %v\n%s", err, output.String())
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Status != "failed" || items[0].ErrorCode != "watch_root_changed" || !watchItemMatches(&items[0], itemSource(item)) {
		t.Fatalf("revoked copy published a ready queue item: %+v %v", items, err)
	}
	after, err := os.ReadFile(alias)
	if err != nil || !bytes.Equal(after, content) {
		t.Fatalf("source bytes changed: %v", err)
	}
}

func watchCopyChildRun(t *testing.T, library string) {
	t.Helper()
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	report := os.NewFile(3, "watch-copy-thread")
	defer report.Close()
	if _, err := fmt.Fprintln(report, unix.Gettid()); err != nil {
		t.Fatal(err)
	}
	var start [1]byte
	if _, err := os.Stdin.Read(start[:]); err != nil {
		t.Fatal(err)
	}
	item, err := s.PrepareNext(t.Context(), 0)
	if err != nil || item == nil || item.Status != "failed" || item.ErrorCode != "watch_root_changed" || item.ContentHash != "" {
		t.Fatalf("revoked native copy result: %+v %v", item, err)
	}
}

func waitWatchCopyStop(ctx context.Context, tid int) (unix.WaitStatus, error) {
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(tid, &status, unix.WALL|unix.WNOHANG, nil)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return status, err
		}
		if pid == tid {
			if !status.Stopped() {
				return status, fmt.Errorf("copy thread terminated before native barrier: %v", status)
			}
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
