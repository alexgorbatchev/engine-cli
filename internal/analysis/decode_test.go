package analysis

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTools installs ffmpeg and ffprobe scripts that print the given output, or fail with stderr when exit is nonzero.
func fakeTools(t *testing.T, ffmpeg, ffprobe string) {
	t.Helper()
	bin := t.TempDir()
	for name, script := range map[string]string{"ffmpeg": ffmpeg, "ffprobe": ffprobe} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func pcmFile(t *testing.T, samples []float32, extra ...byte) string {
	t.Helper()
	raw := make([]byte, 0, 4*len(samples)+len(extra))
	for _, s := range samples {
		raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(s))
	}
	path := filepath.Join(t.TempDir(), "audio.pcm")
	if err := os.WriteFile(path, append(raw, extra...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFileDecodesThroughFFmpeg(t *testing.T) {
	samples, _ := synthesize(0.2, aMinorProgression, section{bpm: 126, bars: 24})
	fakeTools(t, "cat "+pcmFile(t, samples), "exit 1")
	result, err := File(context.Background(), "song.mp3", Options{Tempo: true, Key: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tempo == nil || math.Abs(result.Tempo.BPM-126) > 0.05 || result.Key == nil || result.Key.Camelot != "8A" {
		t.Fatalf("result=%+v tempo=%+v key=%+v", result, result.Tempo, result.Key)
	}
	if math.Abs(result.Duration-float64(len(samples))/SampleRate) > 1e-9 {
		t.Fatalf("duration=%v", result.Duration)
	}
	if _, err := File(context.Background(), "song.mp3", Options{}); err == nil {
		t.Fatal("accepted empty options")
	}
}

func TestDecodeReportsDecoderFailures(t *testing.T) {
	for name, c := range map[string]struct {
		script string
		want   string
	}{
		"exit":         {"echo 'Invalid data found when processing input' >&2; exit 1", "Invalid data found"},
		"incomplete":   {"printf 'abc'", "incomplete sample"},
		"not a number": {"cat " + pcmFile(t, []float32{float32(math.NaN())}), "invalid samples"},
		"huge":         {"cat " + pcmFile(t, []float32{1e7}), "invalid samples"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeTools(t, c.script, "exit 1")
			_, err := File(context.Background(), "song.mp3", Options{Tempo: true})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := File(context.Background(), "song.mp3", Options{Tempo: true}); err == nil || !strings.Contains(err.Error(), "install FFmpeg") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		fakeTools(t, "cat "+pcmFile(t, make([]float32, SampleRate)), "exit 1")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := File(ctx, "song.mp3", Options{Tempo: true}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestLimitedOutputKeepsTheFirstKilobyte(t *testing.T) {
	var out limitedOutput
	for range 3 {
		if n, err := out.Write([]byte(strings.Repeat("x", 600))); n != 600 || err != nil {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
	if len(out.String()) != stderrLimit {
		t.Fatalf("kept %d bytes", len(out.String()))
	}
}

func TestNativeSampleRate(t *testing.T) {
	for name, c := range map[string]struct {
		script string
		rate   float64
		ok     bool
	}{
		"rate":      {`echo '{"streams":[{"sample_rate":"48000"}]}'`, 48000, true},
		"no stream": {`echo '{"streams":[]}'`, 0, false},
		"bad rate":  {`echo '{"streams":[{"sample_rate":"0"}]}'`, 0, false},
		"not json":  {`echo 'nope'`, 0, false},
		"failure":   {"exit 1", 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			fakeTools(t, "exit 1", c.script)
			rate, err := NativeSampleRate(context.Background(), "song.mp3")
			if (err == nil) != c.ok || rate != c.rate {
				t.Fatalf("rate=%v err=%v", rate, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fakeTools(t, "exit 1", "sleep 1")
	if _, err := NativeSampleRate(ctx, "song.mp3"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
