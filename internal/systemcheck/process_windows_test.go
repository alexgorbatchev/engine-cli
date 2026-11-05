package systemcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeSnapshotIncludesCurrentProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processes, err := listProcesses(context.Background())
	if err != nil {
		t.Fatalf("enumerating native processes: %v", err)
	}
	matches := 0
	for _, p := range processes {
		if p.pid != os.Getpid() {
			continue
		}
		matches++
		if !strings.EqualFold(p.name, filepath.Base(executable)) {
			t.Errorf("current process name = %q, want %q", p.name, filepath.Base(executable))
		}
	}
	if matches != 1 {
		t.Fatalf("current PID %d appeared %d times in %d native processes", os.Getpid(), matches, len(processes))
	}
}

func TestNativeSnapshotCancellation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer stop()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{"canceled", canceled, context.Canceled},
		{"expired", expired, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processes, err := listProcesses(tc.ctx)
			if !errors.Is(err, tc.err) {
				t.Fatalf("snapshot error = %v, want %v", err, tc.err)
			}
			if processes != nil {
				t.Fatalf("canceled snapshot returned %d processes", len(processes))
			}
		})
	}
}
