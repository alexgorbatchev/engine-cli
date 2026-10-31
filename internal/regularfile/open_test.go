package regularfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRetainsRegularFile(t *testing.T) {
	for _, rooted := range []bool{false, true} {
		t.Run(map[bool]string{false: "path", true: "root"}[rooted], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "audio")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			open := Open
			name := path
			if rooted {
				name = "audio"
				open = func(name string) (*os.File, error) { return OpenRoot(root, name) }
			}
			file, err := open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if data, err := io.ReadAll(file); err != nil || string(data) != "original" {
				t.Fatalf("retained data=%q err=%v", data, err)
			}
			if _, err := open(filepath.Join(name, "missing")); err == nil {
				t.Fatal("opened nonexistent child")
			}
		})
	}
}

func TestRejectedDescriptorsAreClosed(t *testing.T) {
	for _, kind := range []string{"directory", "pipe", "closed"} {
		t.Run(kind, func(t *testing.T) {
			var file *os.File
			var err error
			switch kind {
			case "directory":
				file, err = os.Open(t.TempDir())
			case "pipe":
				var writer *os.File
				file, writer, err = os.Pipe()
				if err == nil {
					defer writer.Close()
				}
			case "closed":
				file, err = os.CreateTemp(t.TempDir(), "closed")
				if err == nil {
					err = file.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := accept(file, nil); err == nil || got != nil {
				t.Fatalf("accepted %s: %v %v", kind, got, err)
			}
			if err := file.Close(); !errors.Is(err, fs.ErrClosed) {
				t.Fatalf("rejected %s descriptor remains open: %v", kind, err)
			}
		})
	}
}
