package audiosetup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInstallColdAndWarm(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(strconv.FormatBool(cached), func(t *testing.T) {
			f := newSetupFixture(t)
			f.identify(t)
			if cached {
				f.cached(t)
			}
			if out, err := f.run(t, "install"); err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			installed := strings.TrimSpace(string(readTestFile(t, f.pathOutput)))
			if filepath.Dir(installed) != f.scratch {
				t.Fatalf("installation escaped runner scratch: %q", installed)
			}
			for _, tool := range []string{"ffmpeg", "ffprobe"} {
				if !bytes.Equal(readTestFile(t, filepath.Join(installed, tool)), f.tool) {
					t.Errorf("installed %s differs from verified archive", tool)
				}
				info, err := os.Lstat(filepath.Join(installed, tool))
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
					t.Errorf("installed %s is not a regular executable: %v", tool, err)
				}
			}
			events := string(readTestFile(t, f.events))
			wantDownloads := 2
			if cached {
				wantDownloads = 0
			}
			if got := strings.Count(events, "download "); got != wantDownloads {
				t.Errorf("downloads = %d, want %d\n%s", got, wantDownloads, events)
			}
			for _, required := range []string{"execute ffmpeg -version", "execute ffprobe -version", "execute ffmpeg -hide_banner -encoders"} {
				if !strings.Contains(events, required) {
					t.Errorf("missing behavior %q\n%s", required, events)
				}
			}
			if string(readTestFile(t, filepath.Join(f.scratch, "unowned"))) != "preserve" {
				t.Error("setup changed unrelated runner content")
			}
		})
	}
}

func TestInstallRejectsInvalidArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveEntry
		raw     []byte
	}{
		{name: "not a zip", raw: []byte("not an archive")},
		{name: "empty archive"},
		{name: "wrong entry", entries: []archiveEntry{{"other", []byte("unexpected"), 0755}}},
		{name: "extra entry", entries: []archiveEntry{{"ffprobe", []byte("fixture"), 0755}, {"extra", nil, 0600}}},
		{name: "nested entry", entries: []archiveEntry{{"bin/ffprobe", nil, 0755}}},
		{name: "path traversal", entries: []archiveEntry{{"../ffprobe", nil, 0755}}},
		{name: "symlink", entries: []archiveEntry{{"ffprobe", []byte("ffmpeg"), os.ModeSymlink | 0777}}},
		{name: "not executable", entries: []archiveEntry{{"ffprobe", []byte("fixture"), 0644}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSetupFixture(t)
			if tt.raw != nil {
				f.archiveBytes(t, "ffprobe", tt.raw)
			} else {
				f.archive(t, "ffprobe", tt.entries)
			}
			f.cached(t)
			if out, err := f.run(t, "install"); err == nil {
				t.Fatalf("invalid archive was accepted\n%s", out)
			}
			f.failedCleanly(t)
			if events := string(optionalTestFile(t, f.events)); strings.Contains(events, "execute ") || strings.Contains(events, "download ") {
				t.Errorf("rejected cached archive caused execution/download\n%s", events)
			}
		})
	}
}

func TestCachedPairIntegrityPrecedesExtraction(t *testing.T) {
	for _, kind := range []string{"corrupt", "missing", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newSetupFixture(t)
			f.cached(t)
			path := filepath.Join(f.cache, "ffprobe.zip")
			if kind == "corrupt" {
				expected := f.manifest.Assets["ffprobe"]
				f.archive(t, "ffprobe", []archiveEntry{{"ffprobe", append(bytes.Clone(f.tool), []byte("\n# altered cached binary\n")...), 0755}})
				writeTestFile(t, path, readTestFile(t, filepath.Join(f.downloads, "ffprobe.zip")), 0600)
				f.manifest.Assets["ffprobe"] = expected
				f.saveManifest(t)
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(filepath.Join(f.downloads, "ffprobe.zip"), path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if out, err := f.run(t, "install"); err == nil {
				t.Fatalf("invalid cached pair was accepted\n%s", out)
			}
			f.failedCleanly(t)
			events := string(optionalTestFile(t, f.events))
			for _, forbidden := range []string{"unzip -q", "execute ", "download ", "codesign "} {
				if strings.Contains(events, forbidden) {
					t.Errorf("invalid cache reached %q\n%s", forbidden, events)
				}
			}
		})
	}
}

func TestInstallFailuresDoNotPublishOrLeak(t *testing.T) {
	tests := []struct {
		name, variable, value string
		beforeExecution       bool
	}{
		{"unsupported OS", "AUDIO_TEST_OS", "Linux", true},
		{"unsupported architecture", "AUDIO_TEST_ARCH", "x86_64", true},
		{"download fails", "AUDIO_TEST_DOWNLOAD_FAIL", "ffprobe.zip", true},
		{"wrong binary architecture", "AUDIO_TEST_BINARY_ARCH", "x86_64", true},
		{"signature fails", "AUDIO_TEST_NATIVE_FAIL", "codesign:ffprobe", true},
		{"link inspection fails", "AUDIO_TEST_NATIVE_FAIL", "otool:ffprobe", true},
		{"non-system dylib", "AUDIO_TEST_DYLIB", "/opt/homebrew/lib/libcodec.dylib", true},
		{"relative dylib", "AUDIO_TEST_DYLIB", "@rpath/libcodec.dylib", true},
		{"wrong version", "AUDIO_TEST_VERSION", "0.0.0", false},
		{"missing encoder", "AUDIO_TEST_OMIT", "libopus", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSetupFixture(t)
			f.env[tt.variable] = tt.value
			if out, err := f.run(t, "install"); err == nil {
				t.Fatalf("failed prerequisite was accepted\n%s", out)
			}
			f.failedCleanly(t)
			if events := string(optionalTestFile(t, f.events)); tt.beforeExecution && strings.Contains(events, "execute ") {
				t.Errorf("binary executed before prerequisite validation\n%s", events)
			}
		})
	}
}

func TestCacheIdentityTracksArchivePinsAndRecipe(t *testing.T) {
	f := newSetupFixture(t)
	initial := f.identify(t)
	f.archive(t, "ffprobe", []archiveEntry{{"ffprobe", append(bytes.Clone(f.tool), []byte("\n# different archive\n")...), 0755}})
	changedPin := f.identify(t)
	writeTestFile(t, f.script, append(readTestFile(t, f.script), []byte("\n# changed verification recipe\n")...), 0700)
	changedRecipe := f.identify(t)
	if initial == changedPin || changedPin == changedRecipe || initial == changedRecipe {
		t.Fatalf("cache identity did not invalidate: %q, %q, %q", initial, changedPin, changedRecipe)
	}
	if again := f.identify(t); again != changedRecipe {
		t.Fatalf("unchanged recipe identity is unstable: %q != %q", again, changedRecipe)
	}
}

func TestCancellationOwnsActiveDownload(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			f := newSetupFixture(t)
			f.env["AUDIO_TEST_DOWNLOAD_BLOCK"] = "ffprobe.zip"
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := f.command(ctx, "install")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				cancel()
				if !waited {
					_ = cmd.Wait() // Cancellation cleanup; the triggering failure is already reported.
				}
			}()
			pidFile := filepath.Join(f.state, "downloader.pid")
			for len(optionalTestFile(t, pidFile)) == 0 {
				if ctx.Err() != nil {
					t.Fatal("downloader never became ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(readTestFile(t, pidFile))))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			// Signal only the entry process. Group cancellation would hide a
			// script that leaves its downloader alive or defers its cleanup.
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err == nil || ctx.Err() != nil || time.Since(start) > 3*time.Second {
				t.Fatalf("cancellation did not terminate promptly: %v (%s)\n%s", err, time.Since(start), &output)
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Signal(syscall.Signal(0)); err == nil {
				t.Errorf("downloader %d survived entry-process cancellation", pid)
			}
			if err := process.Release(); err != nil {
				t.Error(err)
			}
			f.failedCleanly(t)
		})
	}
}
