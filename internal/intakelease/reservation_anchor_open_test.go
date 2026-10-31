package intakelease

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// This isolates the native anchor creation primitive used by acquire. Both
// retained directories survive every attempt, and no anchor is ever removed.
func TestReservationNativeConcurrentAnchorCreation(t *testing.T) {
	const rounds = 256
	path := t.TempDir()
	var roots [2]*os.Root
	for i := range roots {
		root, err := os.OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		roots[i] = root
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		identity, err := rootIdentity(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("retained root %d identity=%s", i, identity)
	}
	t.Logf("native platform=%s/%s go=%s rounds=%d", runtime.GOOS, runtime.GOARCH, runtime.Version(), rounds)
	for round := range rounds {
		name := fmt.Sprintf("anchor-%d", round)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var files [2]*os.File
		var failures [2]error
		for i, root := range roots {
			wg.Go(func() {
				<-start
				files[i], failures[i] = root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
			})
		}
		close(start)
		wg.Wait()
		for i, failure := range failures {
			if failure == nil {
				continue
			}
			info, observed := roots[i].Lstat(name)
			identity, identityErr := rootIdentity(roots[i])
			t.Errorf("round=%d contender=%d phase=Root.OpenFile flags=O_CREATE|O_RDWR error=%v retainedRoot=%s identityError=%v currentEntry=%v statError=%v", round, i, failure, identity, identityErr, info, observed)
		}
		if files[0] != nil && files[1] != nil {
			first, err := files[0].Stat()
			if err != nil {
				t.Error(err)
			}
			second, err := files[1].Stat()
			if err != nil {
				t.Error(err)
			}
			if first != nil && second != nil && !os.SameFile(first, second) {
				t.Errorf("concurrent anchor opens reached distinct native files: %s", filepath.Join(path, name))
			}
		}
		for _, file := range files {
			if file != nil {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}
		}
		if t.Failed() {
			return
		}
	}
}
