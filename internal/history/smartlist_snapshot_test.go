package history

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func historyLeaseTreeEntry(path string, entry fs.DirEntry) (value string, err error) {
	if !entry.IsDir() && !entry.Type().IsRegular() {
		return "", fmt.Errorf("unsupported snapshot fixture type at %s: %s", path, entry.Type())
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	// Windows directory enumeration can retain stale child timestamps. Query
	// each object's own handle, including directories, before comparing it.
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	if before.IsDir() != entry.IsDir() || (!before.IsDir() && !before.Mode().IsRegular()) {
		return "", fmt.Errorf("snapshot fixture type changed at %s", path)
	}
	var sum [sha256.Size]byte
	if before.Mode().IsRegular() {
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		copy(sum[:], h.Sum(nil))
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("snapshot fixture metadata changed while reading %s", path)
	}
	return fmt.Sprintf("%s:%d:%d:%x", after.Mode(), after.Size(), after.ModTime().UnixNano(), sum), nil
}

func TestSmartlistProjectionSnapshotDetectsChanges(t *testing.T) {
	for _, kind := range []string{"content", "length", "mode", "file time", "directory time", "added", "removed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "evidence")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "record")
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1700000000, 0)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			before := historyLeaseTree(t, root)
			var err error
			field := 3
			switch kind {
			case "content", "length":
				data := "after!"
				if kind == "length" {
					data = "longer replacement"
					field = 1
				}
				err = os.WriteFile(path, []byte(data), 0600)
				if err == nil {
					err = os.Chtimes(path, stamp, stamp)
				}
			case "mode":
				err = os.Chmod(path, 0400)
				field = 0
				t.Cleanup(func() {
					if err := os.Chmod(path, 0600); err != nil {
						t.Error(err)
					}
				})
			case "file time", "directory time":
				if kind == "directory time" {
					path = dir
				}
				err = os.Chtimes(path, stamp.Add(time.Minute), stamp.Add(time.Minute))
				field = 2
			case "added":
				path = filepath.Join(dir, "added")
				err = os.WriteFile(path, []byte("new"), 0600)
			case "removed":
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			after := historyLeaseTree(t, root)
			if reflect.DeepEqual(before, after) {
				t.Fatalf("snapshot missed intentional %s mutation", kind)
			}
			if kind == "added" || kind == "removed" {
				_, had := before[path]
				_, has := after[path]
				if had == has || has != (kind == "added") {
					t.Fatalf("snapshot lost path presence: before=%t after=%t", had, has)
				}
				return
			}
			if strings.Split(before[path], ":")[field] == strings.Split(after[path], ":")[field] {
				t.Fatalf("snapshot missed %s field:\n%s", kind, historyLeaseTreeDiff(before, after))
			}
		})
	}
}
