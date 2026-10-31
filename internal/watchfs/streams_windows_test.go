package watchfs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeAlternateStreamsRefuse(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.wav")
	ordinary := []byte("ordinary audio bytes")
	stream := []byte("distinct alternate stream")
	if err := os.WriteFile(path, ordinary, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+":alternate", stream, 0600); err != nil {
		t.Fatal("native stream fixture:", err)
	}
	if got, err := os.ReadFile(path + ":alternate"); err != nil || !bytes.Equal(got, stream) {
		t.Fatalf("native stream positive control: %q %v", got, err)
	}
	dir, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	for _, name := range []string{"source.wav:alternate", "source.wav:alternate:$DATA", "source.wav::$DATA"} {
		file, err := dir.OpenFile(name)
		if file != nil {
			_ = file.Close()
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("stream selector %q admitted: %v", name, err)
		}
	}
	file, err := dir.OpenFile("source.wav")
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close()); err != nil || !bytes.Equal(got, ordinary) {
		t.Fatalf("ordinary file after stream refusal: %q %v", got, err)
	}
}
