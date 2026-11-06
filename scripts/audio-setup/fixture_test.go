package audiosetup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/godeps/v2"
)

type asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type audioManifest struct {
	Build   string           `json:"build"`
	Version string           `json:"version"`
	TeamID  string           `json:"teamID"`
	Assets  map[string]asset `json:"assets"`
}

type setupFixture struct {
	root, script, commands, downloads, scratch, state string
	output, pathOutput, events, cache                 string
	manifest                                          audioManifest
	env                                               map[string]string
	tool                                              []byte
}

// Only platform inspection and downloads are controlled. ZIP extraction and
// SHA-256 validation use the host tools against real generated archives.
func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("macOS setup shell behavior is exercised on POSIX hosts")
	}
	for _, name := range []string{"bash", "jq", "shasum", "unzip"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("required test tool %s: %v", name, err)
		}
	}
	root := filepath.Join(t.TempDir(), "runner with spaces")
	f := &setupFixture{
		root: root, script: filepath.Join(root, "source", "macos.sh"),
		commands: filepath.Join(root, "commands"), downloads: filepath.Join(root, "downloads"),
		scratch: filepath.Join(root, "scratch"), state: filepath.Join(root, "state"),
		output: filepath.Join(root, "output"), pathOutput: filepath.Join(root, "path"),
		events: filepath.Join(root, "events"), env: map[string]string{},
	}
	for _, dir := range []string{filepath.Dir(f.script), f.commands, f.downloads, f.scratch, f.state} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, f.script, readTestFile(t, "macos.sh"), 0700)
	if err := json.Unmarshal(readTestFile(t, "macos.json"), &f.manifest); err != nil {
		t.Fatal(err)
	}
	f.manifest.Build, f.manifest.TeamID = "fixture", "TESTTEAM01"
	f.tool = readTestFile(t, "testdata/audio-tool.sh")
	command := readTestFile(t, "testdata/command.sh")
	for _, name := range []string{"uname", "curl", "unzip", "lipo", "codesign", "otool"} {
		writeTestFile(t, filepath.Join(f.commands, name), command, 0700)
	}
	unzip, err := exec.LookPath("unzip")
	if err != nil {
		t.Fatal(err)
	}
	f.env = map[string]string{
		"PATH":              f.commands + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RUNNER_TOOL_CACHE": filepath.Join(root, "cache"), "RUNNER_TEMP": f.scratch,
		"GITHUB_OUTPUT": f.output, "GITHUB_PATH": f.pathOutput,
		"TMPDIR": f.scratch, "TMP": f.scratch, "TEMP": f.scratch,
		"MACOS_AUDIO_CACHE_HIT": "false", "AUDIO_TEST_EVENTS": f.events,
		"AUDIO_TEST_STATE": f.state, "AUDIO_TEST_DOWNLOADS": f.downloads,
		"AUDIO_TEST_UNZIP": unzip, "AUDIO_TEST_VERSION": f.manifest.Version,
		"BASH_ENV": "", "ENV": "", "UNZIP": "", "UNZIPOPT": "", "LC_ALL": "C",
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		f.archive(t, tool, []archiveEntry{{tool, f.tool, 0755}})
	}
	writeTestFile(t, filepath.Join(f.scratch, "unowned"), []byte("preserve"), 0600)
	return f
}

type archiveEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func (f *setupFixture) archive(t *testing.T, tool string, entries []archiveEntry) {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		w, err := z.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	f.archiveBytes(t, tool, data.Bytes())
}

func (f *setupFixture) archiveBytes(t *testing.T, tool string, data []byte) {
	t.Helper()
	writeTestFile(t, filepath.Join(f.downloads, tool+".zip"), data, 0600)
	f.manifest.Assets[tool] = asset{
		URL: "https://audio.example/" + tool + ".zip", SHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
	}
	f.saveManifest(t)
}

func (f *setupFixture) saveManifest(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(f.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(filepath.Dir(f.script), "macos.json"), data, 0600)
}

func (f *setupFixture) command(ctx context.Context, action string) *exec.Cmd {
	cmd := godeps.CommandContext(ctx, "bash", f.script, action)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "AUDIO_TEST_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	for key, value := range f.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Dir = f.root
	return cmd
}

func (f *setupFixture) run(t *testing.T, action string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	data, err := f.command(ctx, action).CombinedOutput()
	return string(data), err
}

func (f *setupFixture) identify(t *testing.T) string {
	t.Helper()
	writeTestFile(t, f.output, nil, 0600)
	if out, err := f.run(t, "identify"); err != nil {
		t.Fatalf("identify: %v\n%s", err, out)
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(readTestFile(t, f.output))), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid action output %q", line)
		}
		values[key] = value
	}
	if values["key"] == "" || values["path"] == "" {
		t.Fatalf("missing cache outputs: %v", values)
	}
	f.cache = values["path"]
	return values["key"]
}

func (f *setupFixture) cached(t *testing.T) {
	t.Helper()
	f.identify(t)
	if err := os.MkdirAll(f.cache, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		writeTestFile(t, filepath.Join(f.cache, tool+".zip"), readTestFile(t, filepath.Join(f.downloads, tool+".zip")), 0600)
	}
	f.env["MACOS_AUDIO_CACHE_HIT"] = "true"
}

func (f *setupFixture) failedCleanly(t *testing.T) {
	t.Helper()
	if data := optionalTestFile(t, f.pathOutput); len(data) != 0 {
		t.Errorf("failed setup published PATH: %s", data)
	}
	entries, err := os.ReadDir(f.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "unowned" {
		t.Errorf("setup scratch remains: %v", entries)
	}
	if string(readTestFile(t, filepath.Join(f.scratch, "unowned"))) != "preserve" {
		t.Error("setup changed unrelated runner content")
	}
	cache := filepath.Join(f.root, "cache")
	if _, err := os.Stat(cache); os.IsNotExist(err) {
		return
	}
	if err := filepath.WalkDir(cache, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) != ".zip" {
			t.Errorf("partial download remains: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func optionalTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
