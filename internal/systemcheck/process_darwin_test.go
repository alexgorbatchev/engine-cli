package systemcheck

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"testing"
)

func TestNativeSnapshot(t *testing.T) {
	original := processSnapshot
	t.Cleanup(func() { processSnapshot = original })
	processSnapshot = func(name string, _ ...int) ([]unix.KinfoProc, error) {
		if name != "kern.proc.all" {
			t.Fatal(name)
		}
		var row unix.KinfoProc
		row.Proc.P_pid = 123
		copy(row.Proc.P_comm[:], "Engine DJ")
		return []unix.KinfoProc{row}, nil
	}
	r, err := Run(context.Background(), nil)
	if err != nil || !*r.Checks[EngineDesktop].Running {
		t.Fatalf("%+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listProcesses(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	processSnapshot = func(string, ...int) ([]unix.KinfoProc, error) { return nil, errors.New("denied") }
	if _, err := listProcesses(context.Background()); err == nil {
		t.Fatal("ignored error")
	}
}
