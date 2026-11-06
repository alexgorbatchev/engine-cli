package audiosetup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type prebuiltFixture struct {
	*setupFixture
	platform, suffix, archiveName, archiveRoot string
}

var prebuiltPlatforms = []string{"Linux", "Windows"}

func newPrebuiltFixture(t *testing.T, platform string) *prebuiltFixture {
	t.Helper()
	f := &prebuiltFixture{setupFixture: newRunnerFixture(t, "prebuilt.sh"), platform: platform}
	if err := json.Unmarshal(readTestFile(t, "prebuilt.json"), &f.manifest); err != nil {
		t.Fatal(err)
	}
	f.env["AUDIO_TEST_PLATFORM"], f.env["AUDIO_TEST_VERSION"] = platform, f.manifest.Version
	f.env["AUDIO_TEST_ARCH"], f.env["AUDIO_TEST_OS"] = "x86_64", "Linux"
	f.archiveName, f.archiveRoot = "ffmpeg.tar.xz", "ffmpeg-fixture-linux"
	if platform == "Windows" {
		f.env["AUDIO_TEST_CRLF"] = "true"
		f.env["AUDIO_TEST_OS"] = "MINGW64_NT-10.0-26100"
		f.env["AUDIO_TEST_WINDOWS_PATH"] = `Z:\runner with spaces\audio bin`
		f.archiveName, f.archiveRoot, f.suffix = "ffmpeg.zip", "ffmpeg-fixture-windows", ".exe"
	}
	for _, name := range []string{"tar", "xz"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("required test tool %s: %v", name, err)
		}
		if name == "tar" {
			f.env["AUDIO_TEST_TAR"] = path
		}
	}
	for _, name := range []string{"tar", "cygpath"} {
		writeTestFile(t, filepath.Join(f.commands, name), readTestFile(t, "testdata/command.sh"), 0700)
	}
	f.archive(t, f.pair())
	return f
}

func (f *prebuiltFixture) pair() []archiveEntry {
	return []archiveEntry{
		{f.archiveRoot + "/bin/ffmpeg" + f.suffix, f.tool, 0755},
		{f.archiveRoot + "/bin/ffprobe" + f.suffix, f.tool, 0755},
	}
}

func (f *prebuiltFixture) nativeJQ(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("jq")
	if err != nil {
		t.Fatal(err)
	}
	f.env["AUDIO_TEST_JQ"] = path
	writeTestFile(t, filepath.Join(f.commands, "jq"), readTestFile(t, "testdata/command.sh"), 0700)
}

func (f *prebuiltFixture) archive(t *testing.T, entries []archiveEntry) {
	t.Helper()
	var data []byte
	if f.platform == "Windows" {
		data = zipArchive(t, entries)
	} else {
		data = f.tarXZ(t, entries)
	}
	f.archiveBytes(t, data)
}

func (f *prebuiltFixture) tarXZ(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for _, entry := range entries {
		h := &tar.Header{Name: entry.name, Mode: int64(entry.mode.Perm()), Size: int64(len(entry.data)), Typeflag: tar.TypeReg}
		if entry.mode&os.ModeSymlink != 0 {
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, string(entry.data), 0
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := w.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := f.process(ctx, "xz", "--compress", "--stdout")
	defer stopCommand(t, cmd)
	cmd.Stdin = &data
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	compressed, err := cmd.Output()
	if err != nil {
		t.Fatalf("create tar.xz fixture: %v\n%s", err, &stderr)
	}
	return compressed
}

func (f *prebuiltFixture) archiveBytes(t *testing.T, data []byte) {
	t.Helper()
	writeTestFile(t, filepath.Join(f.downloads, f.archiveName), data, 0600)
	f.manifest.Assets[f.platform] = asset{
		URL:    "https://audio.example/" + f.archiveName,
		SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Root: f.archiveRoot,
	}
	f.saveManifest(t)
}

func (f *prebuiltFixture) saveManifest(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(f.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(filepath.Dir(f.script), "prebuilt.json"), data, 0600)
}

func (f *prebuiltFixture) cached(t *testing.T) {
	t.Helper()
	f.identify(t)
	if err := os.MkdirAll(f.cache, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.cache, f.archiveName), readTestFile(t, filepath.Join(f.downloads, f.archiveName)), 0600)
	f.env["AUDIO_CACHE_HIT"] = "true"
}
