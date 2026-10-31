package intakelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReservationRejectsReplacementDuringNativeOpen(t *testing.T) {
	for _, mutation := range []string{"unlink", "directory", "different-file", "anchors", "parent"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			base := filepath.Join(t.TempDir(), "parent")
			anchors := filepath.Join(base, reservationDirectory)
			if err := os.MkdirAll(anchors, 0700); err != nil {
				t.Fatal(err)
			}
			paths := []string{filepath.Join(base, "a"), filepath.Join(base, "z")}
			anchor := filepath.Join(anchors, "z")
			if err := os.WriteFile(anchor, nil, 0600); err != nil {
				t.Fatal(err)
			}
			barrier := holdKernelLease(t, ctx, anchor)
			type acquisition struct {
				owned bool
				err   error
			}
			result := make(chan acquisition, 1)
			go func() {
				lease, err := Replace(ctx, paths...)
				if lease != nil {
					err = errors.Join(errors.New("changed storage acquired ownership"), err, lease.Close())
				}
				result <- acquisition{owned: lease != nil, err: err}
			}()
			barrier.waitBlocked(t)
			retainedAnchors := anchors
			switch mutation {
			case "unlink", "directory", "different-file":
				if err := os.Remove(anchor); err != nil {
					t.Fatal(err)
				}
			case "anchors":
				retainedAnchors = anchors + "-retained"
				if err := os.Rename(anchors, retainedAnchors); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(anchors, 0700); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if err := os.Rename(base, base+"-retained"); err != nil {
					t.Fatal(err)
				}
				retainedAnchors = filepath.Join(base+"-retained", reservationDirectory)
				if err := os.MkdirAll(anchors, 0700); err != nil {
					t.Fatal(err)
				}
			}
			const evidence = "unrelated replacement evidence"
			if mutation == "directory" {
				if err := os.Mkdir(anchor, 0700); err != nil {
					t.Fatal(err)
				}
			} else if mutation != "unlink" {
				if err := os.WriteFile(anchor, []byte(evidence), 0600); err != nil {
					t.Fatal(err)
				}
			}
			barrier.release(t)
			select {
			case got := <-result:
				if got.owned || got.err == nil || errors.Is(got.err, context.DeadlineExceeded) {
					t.Fatalf("changed native storage was not promptly refused: owned=%v err=%v", got.owned, got.err)
				}
			case <-ctx.Done():
				t.Fatal("native acquisition did not join after release")
			}
			if mutation == "unlink" {
				if _, err := os.Lstat(anchor); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refusal recreated removed anchor: %v", err)
				}
			} else if mutation == "directory" {
				entries, err := os.ReadDir(anchor)
				if err != nil || len(entries) != 0 {
					t.Fatalf("replacement directory changed: %v %v", entries, err)
				}
			} else if data, err := os.ReadFile(anchor); err != nil || string(data) != evidence {
				t.Fatalf("unrelated replacement evidence changed: %q %v", data, err)
			}
			// The first path was locked before the kernel reported the second
			// path's blocked open. Check its original inode after root renames.
			file, err := os.OpenFile(filepath.Join(retainedAnchors, "a"), os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if err := errors.Join(lockReservation(file, true), file.Close()); err != nil {
				t.Fatalf("refusal leaked earlier ownership: %v", err)
			}
			if mutation != "unlink" {
				if err := os.Remove(anchor); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := Replace(ctx, paths...)
			if err != nil {
				t.Fatalf("repaired native storage could not reacquire: %v", err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReservationRelativePathRefusesDeletedWorkingDirectory(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)
	if err := os.Remove(working); err != nil {
		t.Fatal(err)
	}
	lease, err := Replace(t.Context(), "library")
	if lease != nil || !errors.Is(err, os.ErrNotExist) {
		if lease != nil {
			if closeErr := lease.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		t.Fatalf("missing working directory acquired relative ownership: %v", err)
	}
}
