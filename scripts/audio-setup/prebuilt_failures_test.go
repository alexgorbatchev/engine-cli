package audiosetup

import (
	"os"
	"testing"
)

func TestPrebuiltRejectsInvalidArchives(t *testing.T) {
	for _, platform := range prebuiltPlatforms {
		for _, kind := range []string{"invalid archive", "missing ffmpeg", "missing ffprobe", "wrong root", "symlink", "not executable"} {
			t.Run(platform+"-emulated/"+kind, func(t *testing.T) {
				f := newPrebuiltFixture(t, platform)
				entries := f.pair()
				switch kind {
				case "missing ffmpeg":
					entries = entries[1:]
				case "missing ffprobe":
					entries = entries[:1]
				case "wrong root":
					entries[1].name = "ffmpeg-other/bin/ffprobe" + f.suffix
				case "symlink":
					entries[1].mode, entries[1].data = os.ModeSymlink|0777, []byte("ffmpeg"+f.suffix)
				case "not executable":
					entries[1].mode = 0644
				}
				f.archive(t, entries)
				if kind == "invalid archive" {
					f.archiveBytes(t, []byte("not an archive"))
				}
				f.cached(t)
				f.rejects(t, "download ", "execute ")
			})
		}
	}
}

func TestPrebuiltFailuresDoNotPublishOrLeak(t *testing.T) {
	tests := []struct {
		name, variable, value string
		beforeExecution       bool
	}{
		{"unsupported OS", "AUDIO_TEST_OS", "FreeBSD", true},
		{"unsupported architecture", "AUDIO_TEST_ARCH", "arm64", true},
		{"wrong encoder version", "AUDIO_TEST_VERSION", "0.0.0", false},
		{"wrong probe version", "AUDIO_TEST_PROBE_VERSION", "0.0.0", false},
		{"missing encoder", "AUDIO_TEST_OMIT", "libopus", false},
		{"missing decoder", "AUDIO_TEST_OMIT", "pcm_f32le", false},
		{"missing muxer", "AUDIO_TEST_OMIT", "mp4", false},
		{"missing demuxer", "AUDIO_TEST_OMIT", "mov,mp4,m4a,3gp,3g2,mj2", false},
		{"missing device", "AUDIO_TEST_OMIT", "lavfi", false},
		{"missing filter", "AUDIO_TEST_OMIT", "asetpts", false},
		{"missing protocol", "AUDIO_TEST_OMIT", "pipe", false},
	}
	for _, platform := range prebuiltPlatforms {
		for _, tt := range tests {
			t.Run(platform+"-emulated/"+tt.name, func(t *testing.T) {
				f := newPrebuiltFixture(t, platform)
				f.env[tt.variable] = tt.value
				var forbidden []string
				if tt.beforeExecution {
					forbidden = []string{"execute ", "download "}
				}
				f.rejects(t, forbidden...)
			})
		}
		t.Run(platform+"-emulated/download fails", func(t *testing.T) {
			f := newPrebuiltFixture(t, platform)
			f.env["AUDIO_TEST_DOWNLOAD_FAIL"] = f.archiveName
			f.rejects(t, "execute ", "tar ", "unzip -q")
		})
	}
}

func TestPrebuiltWindowsPathConversionFailure(t *testing.T) {
	for _, kind := range []string{"failure", "empty result"} {
		t.Run(kind, func(t *testing.T) {
			f := newPrebuiltFixture(t, "Windows")
			if kind == "failure" {
				f.env["AUDIO_TEST_CYGPATH_FAIL"] = "true"
			} else {
				f.env["AUDIO_TEST_WINDOWS_PATH"] = ""
			}
			f.rejects(t)
		})
	}
}
