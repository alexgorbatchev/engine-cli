package intake

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

const watchHandoffChildLibrary = "ENGINE_WATCH_HANDOFF_CHILD_LIBRARY"

func TestWatchHandoffPublicationChild(t *testing.T) {
	library := os.Getenv(watchHandoffChildLibrary)
	if library == "" {
		return
	}
	version, err := strconv.ParseInt(os.Getenv("ENGINE_WATCH_HANDOFF_CHILD_VERSION"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.WatchList(t.Context())
	if err != nil || len(state.Roots) != 1 {
		t.Fatalf("child config: %+v %v", state, err)
	}
	root := state.Roots[0].Path
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	selected, err := openWatchedSource(root, identity, filepath.Join(root, "source.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	if _, err := fmt.Fprintln(os.Stdout, "watch-handoff-ready"); err != nil {
		t.Fatal(err)
	}
	var start [1]byte
	if _, err := os.Stdin.Read(start[:]); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check); err != nil {
		t.Fatal(err)
	}
}

func TestWatchHandoffRecoversAfterQueueCommitAndProcessDeath(t *testing.T) {
	s, library := queueFixture(t)
	selected, version := watchSourceFixture(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWatchHandoffPublicationChild$")
	command.Env = append(os.Environ(), watchHandoffChildLibrary+"="+library, "ENGINE_WATCH_HANDOFF_CHILD_VERSION="+strconv.FormatInt(version, 10))
	command.Stderr = os.Stderr
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			_ = command.Wait() // Every started child must be reaped on assertion failure.
		}
	}()
	line := bufio.NewScanner(out)
	if !line.Scan() || line.Text() != "watch-handoff-ready" {
		t.Fatalf("child readiness: %q %v", line.Text(), line.Err())
	}
	queueDB, err := db.Open(filepath.Join(library, ".engine-cli", companion.IntakeName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer queueDB.Close()
	queueWriter, err := queueDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer queueWriter.Rollback()
	if _, err := queueWriter.ExecContext(ctx, "UPDATE QueueControl SET reason=reason WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	watchDB, err := openWatch(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	defer watchDB.Close()
	if _, err := in.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	waitWatchObservation(t, ctx, func() (bool, error) {
		h, err := readWatchHandoff(ctx, watchDB, version)
		if err != nil {
			return false, nil
		}
		return h.Provenance == "pending" && !h.ItemID.Valid, nil
	})
	// The real queue writer holds the child after durable pending publication.
	// Reserve watch.db before releasing it, so the next link publication blocks
	// after the actual queue INSERT has committed.
	watchWriter, err := watchDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watchWriter.Rollback()
	if _, err := watchWriter.ExecContext(ctx, "UPDATE WatchControl SET revision=revision WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := queueWriter.Commit(); err != nil {
		t.Fatal(err)
	}
	waitWatchObservation(t, ctx, func() (bool, error) {
		items, err := s.List(ctx)
		return len(items) == 1, err
	})
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed handoff child exited successfully")
	}
	waited = true
	if err := watchWriter.Rollback(); err != nil {
		t.Fatal(err)
	}
	h, err := readWatchHandoff(ctx, watchDB, version)
	if err != nil || h.ItemID.Valid || h.Provenance != "pending" {
		t.Fatalf("kill did not retain genuine unlinked publication: %+v %v", h, err)
	}
	before, err := s.List(ctx)
	if err != nil || len(before) != 1 {
		t.Fatalf("actual queue publication: %+v %v", before, err)
	}
	item, provenance, _, err := s.publishWatchHandoff(ctx, version, selected.source, 0, selected.Check)
	if err != nil || item == nil || item.ID != before[0].ID || provenance != "unknown" {
		t.Fatalf("killed handoff recovery: %+v %s %v", item, provenance, err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("recovery duplicated source version: %+v %v", items, err)
	}
	h, err = readWatchHandoff(ctx, watchDB, version)
	if err != nil || !h.ItemID.Valid || h.ItemID.Int64 != item.ID || !watchItemMatches(item, h.Source) {
		t.Fatalf("recovery receipt differs from original queue tuple: %+v %v", h, err)
	}
}

func waitWatchObservation(t *testing.T, ctx context.Context, observe func() (bool, error)) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		done, err := observe()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native publication boundary not observed:", ctx.Err())
		case <-tick.C:
		}
	}
}
