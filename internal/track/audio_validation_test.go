package track

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/testaudio"
)

func TestRequireAudioRequiresReadableFrames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		err    error
		code   string
	}{
		{"missing tool", "", exec.ErrNotFound, "tool_unavailable"},
		{"probe failure", "", errors.New("invalid media"), "track_not_audio"},
		{"malformed", "{", nil, "track_not_audio"},
		{"missing streams", `{}`, nil, "track_not_audio"},
		{"empty streams", `{"streams":[]}`, nil, "track_not_audio"},
		{"empty stream declaration", `{"streams":[{}],"frames":[{"media_type":"audio"}]}`, nil, "track_not_audio"},
		{"no readable frames", `{"streams":[{"codec_type":"audio"}],"frames":[]}`, nil, "track_not_audio"},
		{"video frames", `{"streams":[{"codec_type":"video"}],"frames":[{"media_type":"video"}]}`, nil, "track_not_audio"},
		{"readable audio", `{"streams":[{"codec_type":"audio"}],"frames":[{"media_type":"audio"}]}`, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireAudio(t.Context(), "source.wav", func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.output), tc.err })
			if progress.ErrorCode(err) != tc.code {
				t.Fatalf("RequireAudio code=%q error=%v, want %q", progress.ErrorCode(err), err, tc.code)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost original probe cause: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RequireAudio(ctx, "source.wav"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestAddTrackRejectsNonAudioBeforeMove(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference", true: "move"}[move], func(t *testing.T) {
			database, library, _ := createTestDB(t)
			path := filepath.Join(t.TempDir(), "not-a-song.wav")
			original := []byte("this is a text file, not audio")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := AddTrack(t.Context(), database, library, path, "", move)
			if progress.ErrorCode(err) != "track_not_audio" || result != nil {
				t.Fatalf("non-audio imported: result=%+v error=%v", result, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("source changed: %q %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(library, "Music")); !os.IsNotExist(err) {
				t.Fatalf("refused input created Music: %v", err)
			}
			var count int
			if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
				t.Fatalf("refused input changed collection: %d %v", count, err)
			}
		})
	}
}

func TestAddTrackImportsActualAudio(t *testing.T) {
	database, library, _ := createTestDB(t)
	path := filepath.Join(t.TempDir(), "song.wav")
	if _, err := defaultRunner(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=frequency=277:duration=0.1", "-c:a", "pcm_s16le", path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := AddTrack(t.Context(), database, library, path, "", true)
	if err != nil || result == nil || result.Track.ID <= 0 || !result.Moved {
		t.Fatalf("audio import: %+v %v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(library, "Music", "song.wav"))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("audio bytes changed: %v", err)
	}
}

func TestAddTrackRefusesMissingNativeProbe(t *testing.T) {
	database, library, _ := createTestDB(t)
	path := filepath.Join(t.TempDir(), "source.wav")
	original := testaudio.Write(t, path)
	t.Setenv("PATH", t.TempDir())
	result, err := AddTrack(t.Context(), database, library, path, "", true)
	if result != nil || progress.ErrorCode(err) != "tool_unavailable" || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing probe accepted: %+v %v", result, err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("missing probe changed source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(library, "Music")); !os.IsNotExist(err) {
		t.Fatalf("missing probe moved source: %v", err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM Track").Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing probe wrote collection: %d %v", count, err)
	}
}

func TestRequireAudioNativeFrames(t *testing.T) {
	dir := t.TempDir()
	silent := filepath.Join(dir, "silent.wav")
	if out, err := defaultRunner(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "0.1", "-c:a", "pcm_s16le", silent); err != nil {
		t.Fatalf("generate silence: %v %s", err, out)
	}
	if err := RequireAudio(t.Context(), silent); err != nil {
		t.Fatalf("valid silent audio refused: %v", err)
	}
	delayed := filepath.Join(dir, "delayed.mka")
	if out, err := defaultRunner(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-itsoffset", "10", "-i", silent, "-c:a", "copy", delayed); err != nil {
		t.Fatalf("generate delayed stream: %v %s", err, out)
	}
	if err := RequireAudio(t.Context(), delayed); err != nil {
		t.Fatalf("valid delayed audio refused: %v", err)
	}
	videoDelayed := filepath.Join(dir, "video-delayed.mkv")
	if out, err := defaultRunner(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=duration=12:size=16x16:rate=1", "-itsoffset", "10", "-i", silent, "-map", "0:v", "-map", "1:a", "-c:v", "ffv1", "-c:a", "copy", videoDelayed); err != nil {
		t.Fatalf("generate video with delayed audio: %v %s", err, out)
	}
	if err := RequireAudio(t.Context(), videoDelayed); err != nil {
		t.Fatalf("audio delayed behind video refused: %v", err)
	}
	empty := filepath.Join(dir, "header-only.wav")
	if out, err := defaultRunner(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "0", "-c:a", "pcm_s16le", empty); err != nil {
		t.Fatalf("generate empty stream: %v %s", err, out)
	}
	if err := RequireAudio(t.Context(), empty); progress.ErrorCode(err) != "track_not_audio" {
		t.Fatalf("header without decoded frames accepted: %v", err)
	}
}
