package pcm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func testGeneration(t *testing.T, s *Store, owner string) *generation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(path, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Fd() == ^uintptr(0) {
		t.Fatal("open fixture has an invalid native descriptor")
	}
	g, err := s.add(owner, f, 48000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Release(owner, g.metadata.ID) })
	return g
}

func TestStoreScopeMutationAndExpiry(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	g := testGeneration(t, s, "library-a")
	if _, err := s.Metadata(t.Context(), "library-b", g.metadata.ID); !errors.Is(err, ErrMissing) {
		t.Fatalf("cross-library access: %v", err)
	}
	if _, err := s.Metadata(t.Context(), "library-a", g.metadata.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.file.Name(), []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Metadata(t.Context(), "library-a", g.metadata.ID); !errors.Is(err, ErrChanged) {
		t.Fatalf("mutation: %v", err)
	}
	assertFileClosed(t, g.file)
	g = testGeneration(t, s, "library-a")
	s.expire(time.Now().Add(idleLimit))
	if _, err := s.Metadata(t.Context(), "library-a", g.metadata.ID); !errors.Is(err, ErrMissing) {
		t.Fatalf("expired: %v", err)
	}
}

func TestStoreBoundsAndCancellation(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	var generations []*generation
	for range maxGenerations {
		generations = append(generations, testGeneration(t, s, "library"))
	}
	f, err := os.Open(generations[0].file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "other", f, 48000); !errors.Is(err, ErrBusy) {
		t.Fatalf("generation cap: %v", err)
	}
	assertFileClosed(t, f)
	ctx, _, finish, err := s.acquire(t.Context(), "library", generations[0].metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, finishOther, err := s.acquire(t.Context(), "library", generations[1].metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.acquire(t.Context(), "library", generations[2].metadata.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("worker cap: %v", err)
	}
	if _, _, _, err := s.acquire(t.Context(), "library", generations[0].metadata.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent file offset ownership: %v", err)
	}
	released := make(chan error, 1)
	go func() { released <- s.Release("library", generations[0].metadata.ID) }()
	<-ctx.Done()
	select {
	case <-released:
		t.Fatal("release returned before active owner finished")
	default:
	}
	finish(context.Canceled)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	finishOther(nil)
	s.Close()
	if _, err := s.Metadata(t.Context(), "library", generations[1].metadata.ID); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed store: %v", err)
	}
}

func TestObservedReplacement(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	g := testGeneration(t, s, "library")
	path := g.file.Name()
	if err := os.Rename(path, path+".old"); err != nil {
		// Go opens Windows files without FILE_SHARE_DELETE. The operating system
		// may prevent replacement; the still-current generation remains usable.
		const sharingViolation = syscall.Errno(32)
		if runtime.GOOS != "windows" || !errors.Is(err, sharingViolation) {
			t.Fatal(err)
		}
		if _, err := s.Metadata(t.Context(), "library", g.metadata.ID); err != nil {
			t.Fatalf("denied replacement invalidated source: %v", err)
		}
		if err := os.WriteFile(path, []byte("modified audio"), 0600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Metadata(t.Context(), "library", g.metadata.ID); !errors.Is(err, ErrChanged) {
		t.Fatalf("replacement: %v", err)
	}
}

func TestInvalidWindowsAndSources(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	for _, request := range []struct {
		start int64
		count int
	}{{-1, 1}, {0, 0}, {0, MaxWindowFrames + 1}, {1<<63 - 1, 1}} {
		if _, err := s.Read(t.Context(), "library", "missing", request.start, request.count); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid window: %v", err)
		}
	}
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "library", f, 48000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("directory: %v", err)
	}
	f, err = os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "library", f, 96000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported rate: %v", err)
	}
}

func TestSourceExtentRejectsConflictingObservations(t *testing.T) {
	g := &generation{provenFrames: 100}
	if err := establishEnd(g, 99); !errors.Is(err, ErrTimeline) || g.metadata.LengthFrames != nil {
		t.Fatalf("end below proven PCM: %v %+v", err, g.metadata)
	}
	if err := establishEnd(g, 110); err != nil {
		t.Fatal(err)
	}
	if err := establishEnd(g, 111); !errors.Is(err, ErrTimeline) || *g.metadata.LengthFrames != 110 {
		t.Fatalf("conflicting EOF: %v %+v", err, g.metadata)
	}
}

func TestCreateRejectsClosedFileAndClosedStore(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	g := testGeneration(t, s, "library")
	if err := g.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "library", g.file, 48000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("closed source: %v", err)
	}
	closed := testGeneration(t, s, "library")
	s.Close()
	file, err := os.Open(closed.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "library", file, 48000); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed store: %v", err)
	}
	assertFileClosed(t, file)
}

func assertFileClosed(t *testing.T, file *os.File) {
	t.Helper()
	if descriptor := file.Fd(); descriptor != ^uintptr(0) {
		t.Fatalf("retained native descriptor remains open: %d", descriptor)
	}
}
