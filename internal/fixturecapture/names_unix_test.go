//go:build !windows

package fixturecapture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesUnrepresentableNativeFilename(t *testing.T) {
	base := t.TempDir()
	before, after := filepath.Join(base, "before"), filepath.Join(base, "after")
	for _, path := range []string{before, after} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := filepath.Join(before, "native-"+string([]byte{255}))
	if err := os.WriteFile(name, []byte("evidence must not be renamed by JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(base, "capture")
	if _, err := Capture(t.Context(), before, after, out); err == nil || !strings.Contains(err.Error(), "invalid UTF-8 input filename") {
		t.Fatalf("lossy filename accepted: %v", err)
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "evidence must not be renamed by JSON" {
		t.Fatalf("native input changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "complete.json")); !os.IsNotExist(err) {
		t.Fatalf("refusal published completion: %v", err)
	}
}
