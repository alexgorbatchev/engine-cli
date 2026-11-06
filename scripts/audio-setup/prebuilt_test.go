package audiosetup

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Both platforms use controlled shell executables. Windows runs with emulated
// uname/cygpath and CRLF output on a POSIX host, not native Windows binaries.
func TestPrebuiltColdAndWarm(t *testing.T) {
	for _, platform := range prebuiltPlatforms {
		for _, cached := range []bool{false, true} {
			t.Run(platform+"-emulated/"+strconv.FormatBool(cached), func(t *testing.T) {
				f := newPrebuiltFixture(t, platform)
				entries := append(f.pair(), archiveEntry{f.archiveRoot + "/doc/extra", []byte("not selected"), 0600},
					archiveEntry{f.archiveRoot + "/bin/ffplay" + f.suffix, f.tool, 0755})
				f.archive(t, entries)
				f.install(t, cached)
			})
		}
	}
}

func TestPrebuiltWindowsCacheBackslashes(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run("Windows-emulated/"+strconv.FormatBool(cached), func(t *testing.T) {
			f := newPrebuiltFixture(t, "Windows")
			// A real POSIX filename containing backslashes reproduces GNU checksum
			// filename escaping without faking the hasher's output.
			f.env["RUNNER_TOOL_CACHE"] = filepath.Join(f.root, `D:\hostedtoolcache\windows`)
			f.install(t, cached)
		})
	}
}

func (f *prebuiltFixture) install(t *testing.T, cached bool) {
	t.Helper()
	downloads := 1
	if cached {
		f.cached(t)
		downloads = 0
	}
	if out, err := f.run(t, "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	installed := strings.TrimSpace(string(readTestFile(t, f.pathOutput)))
	if f.platform == "Windows" {
		if installed != f.env["AUDIO_TEST_WINDOWS_PATH"] {
			t.Fatalf("PATH did not use cygpath result: %q", installed)
		}
		installed = strings.TrimSpace(string(readTestFile(t, filepath.Join(f.state, "cygpath-input"))))
	}
	if filepath.Dir(filepath.Dir(filepath.Dir(installed))) != f.scratch {
		t.Fatalf("installation escaped runner scratch: %q", installed)
	}
	f.installed(t, installed, f.suffix, downloads)
	assertSelectedPairOnly(t, f, installed)
}

func assertSelectedPairOnly(t *testing.T, f *prebuiltFixture, installed string) {
	t.Helper()
	installRoot := filepath.Dir(filepath.Dir(installed))
	if err := filepath.WalkDir(installRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path != filepath.Join(installed, "ffmpeg"+f.suffix) && path != filepath.Join(installed, "ffprobe"+f.suffix) {
			t.Errorf("unselected archive entry extracted: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrebuiltCachedArchiveIntegrityPrecedesExtraction(t *testing.T) {
	for _, platform := range prebuiltPlatforms {
		for _, kind := range []string{"tampered", "partial", "missing", "symlink"} {
			t.Run(platform+"-emulated/"+kind, func(t *testing.T) {
				f := newPrebuiltFixture(t, platform)
				f.cached(t)
				path := filepath.Join(f.cache, f.archiveName)
				switch kind {
				case "tampered":
					expected := f.manifest.Assets[platform]
					entries := f.pair()
					entries[1].data = append(bytes.Clone(f.tool), []byte("\n# altered cached binary\n")...)
					f.archive(t, entries)
					writeTestFile(t, path, readTestFile(t, filepath.Join(f.downloads, f.archiveName)), 0600)
					f.manifest.Assets[platform] = expected
					f.saveManifest(t)
				case "partial":
					data := readTestFile(t, path)
					writeTestFile(t, path, data[:len(data)/2], 0600)
				case "missing", "symlink":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if kind == "symlink" {
						if err := os.Symlink(filepath.Join(f.downloads, f.archiveName), path); err != nil {
							t.Fatal(err)
						}
					}
				}
				f.rejects(t, "download ", "execute ", "tar ", "unzip -q")
			})
		}
	}
}

func TestPrebuiltCacheIdentityTracksInputs(t *testing.T) {
	for _, platform := range prebuiltPlatforms {
		t.Run(platform+"-emulated", func(t *testing.T) {
			f := newPrebuiltFixture(t, platform)
			f.cached(t)
			keys := map[string]bool{f.identify(t): true}
			entries := f.pair()
			entries[0].data = append(bytes.Clone(f.tool), []byte("\n# changed archive\n")...)
			f.archive(t, entries)
			keys[f.identify(t)] = true
			f.rejects(t, "download ", "execute ", "tar ", "unzip -q")
			var finalKey string
			for _, path := range []string{f.script, filepath.Join(filepath.Dir(f.script), "common.sh")} {
				writeTestFile(t, path, append(readTestFile(t, path), []byte("\n# changed recipe\n")...), 0700)
				finalKey = f.identify(t)
				keys[finalKey] = true
			}
			if len(keys) != 4 {
				t.Fatalf("cache identity did not invalidate: %v", keys)
			}
			if key := f.identify(t); key != finalKey {
				t.Fatalf("unchanged recipe identity is unstable: %q != %q", key, finalKey)
			}
		})
	}
}

func TestPrebuiltCancellationOwnsActiveDownload(t *testing.T) {
	for _, platform := range prebuiltPlatforms {
		for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(platform+"-emulated/"+signal.String(), func(t *testing.T) {
				f := newPrebuiltFixture(t, platform)
				f.env["AUDIO_TEST_DOWNLOAD_BLOCK"] = f.archiveName
				assertDownloadCancellation(t, f.setupFixture, signal)
			})
		}
	}
}
