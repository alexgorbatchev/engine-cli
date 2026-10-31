//go:build pcm_native

package pcm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func nativeWAV(t *testing.T) string {
	t.Helper()
	const rate = 44100
	const frames = rate*9 + 117
	var data bytes.Buffer
	data.WriteString("RIFF")
	for _, value := range []any{uint32(36 + frames*4), []byte("WAVEfmt "), uint32(16), uint16(1), uint16(2), uint32(rate), uint32(rate * 4), uint16(4), uint16(16), []byte("data"), uint32(frames * 4)} {
		if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	for i := range frames {
		for _, frequency := range []float64{277, 617} {
			sample := int16(10000 * math.Sin(2*math.Pi*frequency*float64(i)/rate))
			if err := binary.Write(&data, binary.LittleEndian, sample); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func nativeOutput(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-hide_banner", "-v", "error", "-nostdin"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, stderr.String())
	}
	return data
}

func createNative(t *testing.T, s *Store, path string, rate int) Metadata {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Create(t.Context(), "library", file, rate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Release("library", m.ID) })
	return m
}

func TestNativeSeekableFDWindowsAndEOF(t *testing.T) {
	path := nativeWAV(t)
	for _, rate := range []int{44100, 48000} {
		t.Run(stringRate(rate), func(t *testing.T) {
			s := New()
			t.Cleanup(s.Close)
			m := createNative(t, s, path, rate)
			if m.LengthFrames != nil {
				t.Fatal("capped head probe claimed EOF")
			}
			oracle := nativeOutput(t, "-i", path, "-map", "0:a:0", "-af", resampleFilter(rate, 0), "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
			length := int64(len(oracle) / BytesPerFrame)
			for _, request := range []struct {
				start int64
				count int
			}{{0, 1}, {int64(2*rate) - 1, 3}, {int64(3*rate) + 19, 127}, {length - 100, 200}, {length, 10}, {length + 100, 10}} {
				window, err := s.Read(t.Context(), "library", m.ID, request.start, request.count)
				if err != nil {
					t.Fatalf("request=%+v: %v", request, err)
				}
				end := min(request.start+int64(request.count), length)
				start := min(request.start, length)
				if !bytes.Equal(window.Data, oracle[start*BytesPerFrame:end*BytesPerFrame]) {
					t.Fatalf("sample lattice mismatch request=%+v", request)
				}
			}
			got, err := s.Metadata(t.Context(), "library", m.ID)
			if err != nil || got.LengthFrames == nil || *got.LengthFrames != length {
				t.Fatalf("EOF metadata=%+v error=%v", got, err)
			}
		})
	}
}

func stringRate(rate int) string {
	if rate == 44100 {
		return "44100"
	}
	return "48000"
}

func TestNativeOvershotTailDoesNotProveAbsentFrames(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	m := createNative(t, s, nativeWAV(t), 48000)
	overshot, err := s.Read(t.Context(), "library", m.ID, 10*48000, 1)
	if err != nil || overshot.FrameCount != 0 || overshot.LengthFrames == nil {
		t.Fatalf("overshot tail=%+v error=%v", overshot.Metadata, err)
	}
	// This uncached native anchor observes the same EOF again. Empty overshoot
	// data must not have advanced proof beyond the established source extent.
	earlier, err := s.Read(t.Context(), "library", m.ID, 8*48000, 32)
	if err != nil || earlier.FrameCount != 32 {
		t.Fatalf("earlier count=%d error=%v", earlier.FrameCount, err)
	}
}

func TestNativeCodecPages(t *testing.T) {
	source := nativeWAV(t)
	for _, codec := range []struct{ name, extension, encoder string }{{"MP3", ".mp3", "libmp3lame"}, {"AAC", ".m4a", "aac"}, {"FLAC", ".flac", "flac"}, {"ALAC", ".m4a", "alac"}, {"Vorbis", ".ogg", "libvorbis"}, {"Opus", ".opus", "libopus"}, {"AIFF", ".aiff", "pcm_s16be"}} {
		t.Run(codec.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "encoded"+codec.extension)
			nativeOutput(t, "-i", source, "-c:a", codec.encoder, path)
			for _, rate := range []int{44100, 48000} {
				t.Run(stringRate(rate), func(t *testing.T) { checkNativeCodecPages(t, path, rate) })
			}
		})
	}
}

func TestNativeVariableBitrateMP3(t *testing.T) {
	source := nativeWAV(t)
	path := filepath.Join(t.TempDir(), "variable.mp3")
	nativeOutput(t, "-i", source, "-c:a", "libmp3lame", "-q:a", "2", path)
	for _, rate := range []int{44100, 48000} {
		t.Run(stringRate(rate), func(t *testing.T) { checkNativeCodecPages(t, path, rate) })
	}
}

func checkNativeCodecPages(t *testing.T, path string, rate int) {
	t.Helper()
	s := New()
	t.Cleanup(s.Close)
	m := createNative(t, s, path, rate)
	oracle := nativeOutput(t, "-i", path, "-map", "0:a:0", "-af", resampleFilter(rate, 0), "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
	length := int64(len(oracle) / BytesPerFrame)
	// Both anchors straddle page boundaries. The late anchor must seek natively;
	// overlap/recreation compare one fixed-page realization, not stochastic PNS
	// output against the independently initialized continuous decoder.
	for _, start := range []int64{int64(2*rate) - 3, int64(8*rate) - 3} {
		first, err := s.Read(t.Context(), "library", m.ID, start, 110)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.Read(t.Context(), "library", m.ID, start+4, 70)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(second.Data, first.Data[4*BytesPerFrame:74*BytesPerFrame]) {
			t.Fatal("overlap differs")
		}
		// Clear native page residency to prove the same fixed anchors regenerate
		// a deterministic realization, including codecs with stochastic PNS.
		s.mu.Lock()
		for _, p := range s.items[m.ID].pages {
			s.cacheBytes -= len(p.data)
		}
		s.items[m.ID].pages = make(map[int64]*page)
		s.mu.Unlock()
		repeated, err := s.Read(t.Context(), "library", m.ID, start, 110)
		if err != nil || !bytes.Equal(first.Data, repeated.Data) {
			t.Fatalf("regenerated page differs: %v", err)
		}
	}
	tail, err := s.Read(t.Context(), "library", m.ID, length-31, 100)
	if err != nil || tail.FrameCount != 31 || tail.LengthFrames == nil || *tail.LengthFrames != length {
		t.Fatalf("native tail count=%d length=%v oracle=%d error=%v", tail.FrameCount, tail.LengthFrames, length, err)
	}
	end, err := s.Read(t.Context(), "library", m.ID, length, 1)
	if err != nil || end.FrameCount != 0 || end.LengthFrames == nil || *end.LengthFrames != length {
		t.Fatalf("native EOF=%+v error=%v", end, err)
	}
}

func TestNativeEmptyFarSeekDoesNotInventEOF(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	m := createNative(t, s, nativeWAV(t), 48000)
	if _, err := s.Read(t.Context(), "library", m.ID, 32*48000, 1); !errors.Is(err, ErrExtentUnknown) {
		t.Fatalf("empty far seek=%v", err)
	}
	metadata, err := s.Metadata(t.Context(), "library", m.ID)
	if err != nil || metadata.LengthFrames != nil {
		t.Fatalf("empty probe claimed end=%+v error=%v", metadata, err)
	}
	tail, err := s.Read(t.Context(), "library", m.ID, 8*48000, 96000)
	if err != nil || tail.FrameCount <= 48000 || tail.LengthFrames == nil {
		t.Fatalf("recoverable tail count=%d error=%v", tail.FrameCount, err)
	}
}

func TestNativeAACPresentationOrigin(t *testing.T) {
	source := nativeWAV(t)
	for _, offset := range []string{"0", "0.037"} {
		t.Run(offset, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "offset.m4a")
			nativeOutput(t, "-i", source, "-c:a", "aac", "-output_ts_offset", offset, path)
			checkNativeCodecPages(t, path, 48000)
		})
	}
}

func TestNativeCacheEvictionPreservesActiveWindow(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	m := createNative(t, s, nativeWAV(t), 48000)
	other := testGeneration(t, s, "other")
	// Residency starts full across generations. A real new decoder page must
	// displace the globally oldest entry without changing the active result.
	s.mu.Lock()
	other.pages[0] = &page{data: make([]byte, cacheLimit/2), used: time.Unix(1, 0)}
	other.pages[1] = &page{data: make([]byte, cacheLimit/2), used: time.Unix(2, 0)}
	s.cacheBytes = cacheLimit
	s.mu.Unlock()
	window, err := s.Read(t.Context(), "library", m.ID, 0, 19)
	if err != nil || window.FrameCount != 19 {
		t.Fatalf("window count=%d error=%v", window.FrameCount, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if other.pages[0] != nil || other.pages[1] == nil || s.cacheBytes > cacheLimit {
		t.Fatalf("cache eviction entries=%d bytes=%d", len(other.pages), s.cacheBytes)
	}
}

func TestNativeRejectsDiscontinuousAndCorruptSources(t *testing.T) {
	source := nativeWAV(t)
	path := filepath.Join(t.TempDir(), "gap.m4a")
	nativeOutput(t, "-i", source, "-af", "asetpts=PTS+gte(T\\,2)*10/TB", "-c:a", "aac", path)
	s := New()
	t.Cleanup(s.Close)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "library", file, 48000); !errors.Is(err, ErrTimeline) {
		t.Fatalf("gap: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.m4a")
	if err := os.WriteFile(bad, []byte("invalid audio"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), "library", file, 48000); !errors.Is(err, ErrDecode) {
		t.Fatalf("corruption: %v", err)
	}
}
