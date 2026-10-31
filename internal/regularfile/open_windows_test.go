package regularfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNamedPipeIsRejectedBeforeReading(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\engine-pcm-%d-%s`, os.Getpid(), filepath.Base(t.TempDir()))
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_OUTBOUND, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 1024, 1024, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe)
	if file, err := Open(name); err == nil || file != nil {
		t.Fatalf("accepted named pipe before reading: file=%v err=%v", file, err)
	}
}
