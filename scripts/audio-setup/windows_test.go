package audiosetup

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPrebuiltWindowsNativeJQ(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run("Windows-emulated/"+strconv.FormatBool(cached), func(t *testing.T) {
			f := newPrebuiltFixture(t, "Windows")
			f.nativeJQ(t)
			f.install(t, cached)
		})
	}
}

func TestPrebuiltWindowsNativeJQRejectsInvalidManifest(t *testing.T) {
	for _, data := range []string{`{"build":null}`, `{"build":`} {
		t.Run(data, func(t *testing.T) {
			f := newPrebuiltFixture(t, "Windows")
			f.nativeJQ(t)
			writeTestFile(t, filepath.Join(filepath.Dir(f.script), "prebuilt.json"), []byte(data), 0600)
			out, err := f.run(t, "install")
			if err == nil {
				t.Fatal("invalid manifest was accepted")
			}
			if strings.Contains(out, "Invalid audio archive root") {
				t.Fatalf("jq failure did not stop manifest parsing: %s", out)
			}
			f.failedCleanly(t)
			if events := string(optionalTestFile(t, f.events)); events != "" {
				t.Fatalf("invalid manifest reached installation: %s", events)
			}
		})
	}
}
