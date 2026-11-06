package audiosetup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func (f *setupFixture) installed(t *testing.T, dir, suffix string, downloads int) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		path := filepath.Join(dir, tool+suffix)
		if !bytes.Equal(readTestFile(t, path), f.tool) {
			t.Errorf("installed %s differs from verified archive", tool)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
			t.Errorf("installed %s is not a regular executable: %v", tool, err)
		}
	}
	events := string(readTestFile(t, f.events))
	if got := strings.Count(events, "download "); got != downloads {
		t.Errorf("downloads = %d, want %d\n%s", got, downloads, events)
	}
	for _, required := range []string{"execute ffmpeg" + suffix + " -version", "execute ffprobe" + suffix + " -version", "execute ffmpeg" + suffix + " -hide_banner -encoders"} {
		if !strings.Contains(events, required) {
			t.Errorf("missing behavior %q\n%s", required, events)
		}
	}
	if string(readTestFile(t, filepath.Join(f.scratch, "unowned"))) != "preserve" {
		t.Error("setup changed unrelated runner content")
	}
}

func (f *setupFixture) rejects(t *testing.T, forbidden ...string) {
	t.Helper()
	if out, err := f.run(t, "install"); err == nil {
		t.Fatalf("invalid setup was accepted\n%s", out)
	}
	f.failedCleanly(t)
	events := string(optionalTestFile(t, f.events))
	for _, event := range forbidden {
		if strings.Contains(events, event) {
			t.Errorf("invalid setup reached %q\n%s", event, events)
		}
	}
}

func assertDownloadCancellation(t *testing.T, f *setupFixture, signal syscall.Signal) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := f.command(ctx, "install")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = cmd.Wait() // Cancellation cleanup; the triggering failure is already reported.
		}
		stopCommand(t, cmd)
	}()
	pidFile := filepath.Join(f.state, "downloader.pid")
	for len(optionalTestFile(t, pidFile)) == 0 {
		if ctx.Err() != nil {
			t.Fatal("downloader never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(readTestFile(t, pidFile))))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	// Signal only the entry process. Group cancellation would hide a
	// script that leaves its downloader alive or defers its cleanup.
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil || ctx.Err() != nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation did not terminate promptly: %v (%s)\n%s", err, time.Since(start), &output)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Errorf("downloader %d survived entry-process cancellation", pid)
	}
	if err := process.Release(); err != nil {
		t.Error(err)
	}
	f.failedCleanly(t)
}

func stopCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	// The context watcher may have finished after Wait. The godeps Cancel
	// function still owns bounded termination of any remaining group members.
	if err := cmd.Cancel(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("stop owned command group: %v", err)
	}
}
