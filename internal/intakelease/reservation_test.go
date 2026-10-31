package intakelease

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestReservationNativeNameResolution(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "LibraryLongName")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := reservePaths(t.Context(), false, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReservation(t, r)
	alias := filepath.Join(parent, strings.ToLower(filepath.Base(path)))
	if _, err := os.Stat(alias); err == nil {
		assertReserved(t, alias)
	} else if errors.Is(err, os.ErrNotExist) {
		other, err := reservePaths(t.Context(), true, alias)
		if err != nil {
			t.Fatalf("distinct case-sensitive child blocked: %v", err)
		}
		closeReservation(t, other)
	} else {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("coordination wrote inside library: %v, %v", entries, err)
	}
}

func TestReservationRefusesFileWithoutEnrolment(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "not-a-library")
	if err := os.WriteFile(path, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := reservePaths(t.Context(), true, path); err == nil || r != nil {
		t.Fatalf("file acquired a library reservation: %v, %v", r, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("invalid library enrolled coordination: %v, %v", entries, err)
	}
}

func closeReservation(t *testing.T, r *reservation) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReservationChild(t *testing.T) {
	path := os.Getenv("ENGINE_INTAKE_RESERVATION_CHILD")
	if path == "" {
		return
	}
	r, err := reservePaths(t.Context(), false, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReservation(t, r)
	if _, err := fmt.Fprintln(os.Stdout, "reserved"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestReservationForeignOwners(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "library")
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReservationChild$")
			child.Env = append(os.Environ(), "ENGINE_INTAKE_RESERVATION_CHILD="+path)
			child.Stderr = os.Stderr
			input, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			t.Cleanup(func() {
				if !joined {
					if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Error(err)
					}
					if err := child.Wait(); err == nil {
						t.Error("unjoined child unexpectedly succeeded")
					}
				}
			})
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || scanner.Text() != "reserved" {
				t.Fatalf("child readiness: %q %v", scanner.Text(), scanner.Err())
			}
			second, err := reservePaths(ctx, false, path)
			if err != nil {
				t.Fatal(err)
			}
			defer closeReservation(t, second)
			assertReserved(t, path)
			if kill {
				if err := child.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := input.Close(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			joined = true
			if (err != nil) != kill {
				t.Fatalf("child exit after kill=%t: %v", kill, err)
			}
			assertReserved(t, path)
			closeReservation(t, second)
			r, err := reservePaths(ctx, true, path)
			if err != nil {
				t.Fatalf("last owner release: %v", err)
			}
			closeReservation(t, r)
		})
	}
}

func assertReserved(t *testing.T, path string) {
	t.Helper()
	r, err := reservePaths(t.Context(), true, path)
	if r != nil {
		closeReservation(t, r)
	}
	if progress.ErrorCode(err) != "intake_replacing" {
		t.Fatalf("exclusive reservation bypassed owner: %v", err)
	}
}

func TestReservationSurvivesRenameAndReservesVacantDestination(t *testing.T) {
	parent := t.TempDir()
	source, destination := filepath.Join(parent, "source"), filepath.Join(parent, "destination")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := reservePaths(t.Context(), true, destination, source)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReservation(t, r)
	assertReserved(t, source)
	assertReserved(t, destination)
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source retained a tombstone: %v", err)
	}
	assertReserved(t, source)
	assertReserved(t, destination)
	for _, path := range []string{source, destination} {
		info, err := os.Stat(filepath.Join(parent, reservationDirectory, filepath.Base(path)))
		if err != nil || info.Size() != 0 {
			t.Fatalf("coordinator contains data: %v %v", info, err)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 || entries[0].Name() != "payload" {
		t.Fatalf("coordinator entered moved library: %v %v", entries, err)
	}
	closeReservation(t, r)
	reverse, err := reservePaths(t.Context(), true, source, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReservation(t, reverse)
	if err := os.Rename(destination, source); err != nil {
		t.Fatal(err)
	}
	assertReserved(t, source)
	assertReserved(t, destination)
}

func TestReservationRefusesUnsafeAnchors(t *testing.T) {
	for _, kind := range []string{"directory", "data", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			anchors := filepath.Join(parent, reservationDirectory)
			if err := os.Mkdir(anchors, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "library")
			anchor := filepath.Join(anchors, "library")
			switch kind {
			case "directory":
				if err := os.Mkdir(anchor, 0700); err != nil {
					t.Fatal(err)
				}
			case "data":
				if err := os.WriteFile(anchor, []byte("not coordination"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(parent, "unrelated")
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, anchor); err != nil {
					t.Fatal(err)
				}
			}
			if r, err := reservePaths(t.Context(), false, path); r != nil || err == nil {
				if r != nil {
					closeReservation(t, r)
				}
				t.Fatalf("unsafe anchor accepted: %v", err)
			}
		})
	}
}

func TestReservationCancelledWithoutEnrolment(t *testing.T) {
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r, err := reservePaths(ctx, false, filepath.Join(parent, "library")); r != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reservation: %v %v", r, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled reservation enrolled: %v %v", entries, err)
	}
}

func TestReservationReverseAcquisitionDoesNotLeakPartialOwner(t *testing.T) {
	parent := t.TempDir()
	a, b := filepath.Join(parent, "a"), filepath.Join(parent, "b")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *reservation, 2)
	for _, paths := range [][]string{{a, b}, {b, a}} {
		wg.Go(func() {
			<-start
			r, err := reservePaths(t.Context(), true, paths...)
			if err != nil && progress.ErrorCode(err) != "intake_replacing" {
				t.Errorf("reverse acquisition: %v", err)
			}
			results <- r
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var winners int
	for r := range results {
		if r != nil {
			winners++
			closeReservation(t, r)
		}
	}
	if winners != 1 {
		t.Fatalf("exclusive pair owners=%d", winners)
	}
	r, err := reservePaths(t.Context(), true, a, b)
	if err != nil {
		t.Fatalf("failed contender leaked ownership: %v", err)
	}
	closeReservation(t, r)
}
