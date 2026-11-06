package intake

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/watchclock"
)

const watchFenceChild = "ENGINE_WATCH_FENCE_CHILD"
const watchFenceReady = "watch fence committed\n"

func TestWatchAuthorityFenceChild(t *testing.T) {
	library := os.Getenv(watchFenceChild)
	if library == "" {
		return
	}
	floor, err := strconv.ParseInt(os.Getenv("ENGINE_WATCH_FENCE_REVISION"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := intakestate.With(context.Background(), library, func(ctx context.Context) error {
		if _, err := watchclock.Restore(ctx, library, floor); err != nil {
			return err
		}
		if os.Getenv("ENGINE_WATCH_FENCE_ABSENT") == "1" {
			if err := companion.Remove(filepath.Join(library, ".engine-cli", companion.WatchName)); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(os.Stdout, watchFenceReady); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, os.Stdin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func killWatchFenceOwner(t *testing.T, library string, revision int64, absent bool) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWatchAuthorityFenceChild$")
	absence := "0"
	if absent {
		absence = "1"
	}
	command.Env = append(os.Environ(), watchFenceChild+"="+library,
		"ENGINE_WATCH_FENCE_REVISION="+strconv.FormatInt(revision, 10),
		"ENGINE_WATCH_FENCE_ABSENT="+absence)
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
		buffer := make([]byte, len(watchFenceReady))
		_, err := io.ReadFull(out, buffer)
		if err == nil && string(buffer) != watchFenceReady {
			err = fmt.Errorf("unexpected watch fence receipt: %q", buffer)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not commit restore fence")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed restore owner reported success")
	}
	joined = true
}

func TestWatchAuthorityFencesProcessDeathAndAbsence(t *testing.T) {
	for _, image := range []string{"old-config", "prewatch-absence"} {
		t.Run(image, func(t *testing.T) {
			s, library := queueFixture(t)
			root := t.TempDir()
			state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
			if err != nil {
				t.Fatal(err)
			}
			killWatchFenceOwner(t, library, state.Revision, image == "prewatch-absence")
			reopened, err := WatchList(t.Context(), library)
			if err != nil || !reopened.Suspended || reopened.Reason != "restored" || reopened.Revision <= state.Revision {
				t.Fatalf("replacement fence did not survive reopen: %+v %v", reopened, err)
			}
			if _, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Revision: 0}); err == nil {
				t.Fatal("prewatch stale Add consent became valid")
			}
			if image == "prewatch-absence" {
				if _, err := os.Stat(filepath.Join(library, ".engine-cli", companion.WatchName)); !os.IsNotExist(err) {
					t.Fatal("stale Add enrolled an absent image:", err)
				}
			}
			configured, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Revision: reopened.Revision})
			if err != nil || !configured.Suspended || configured.Reason != "restored" {
				t.Fatalf("ordinary config implicitly activated restored paths: %+v %v", configured, err)
			}
			active, err := s.WatchActivate(t.Context(), configured.Revision)
			if err != nil || active.Suspended || active.Reason != "" {
				t.Fatalf("explicit activation failed: %+v %v", active, err)
			}
		})
	}
}
