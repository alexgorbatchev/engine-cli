// Package audiodecode streams first-audio PCM with bounded memory and cancellation.
package audiodecode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const (
	stderrLimit = 1024
	blockBytes  = 64 * 1024
)

// Stream decodes the first audio stream as mono float32 samples at the requested rate.
func Stream(ctx context.Context, path string, rate float64, feed func([]float32) error) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", "file:"+path, "-map", "0:a:0", "-ac", "1", "-ar", fmt.Sprint(rate), "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
	cmd.WaitDelay = 5 * time.Second
	var stderr limitedOutput
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting audio decoder; install FFmpeg and retry: %w", err)
	}
	readErr := readSamples(ctx, stdout, feed)
	if readErr != nil {
		// Launchers can leave a decoder child holding the pipes after cancellation.
		// Closing its output releases a producer blocked on an unread PCM block.
		_ = stdout.Close()
		_ = cmd.Process.Kill() // Stop the producer before waiting after a read failure.
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("decoding audio: %s: %w", progress.PublicError(errors.New(strings.TrimSpace(stderr.String()))), waitErr)
	}
	return nil
}

// NativeExtent counts decoded first-audio sample frames at that stream's own rate.
// Container durations and stream duration metadata do not determine the sample count.
func NativeExtent(ctx context.Context, path string) (float64, int64, error) {
	rate, err := NativeSampleRate(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || math.Trunc(rate) != rate {
		return 0, 0, errors.New("the audio stream has no usable native sample rate")
	}
	var count int64
	err = Stream(ctx, path, rate, func(samples []float32) error {
		if int64(len(samples)) > 1<<53-count {
			return errors.New("decoded audio exceeds the exact native sample extent")
		}
		count += int64(len(samples))
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if count == 0 {
		return 0, 0, errors.New("the audio stream has no decoded samples")
	}
	return rate, count, nil
}

func readSamples(ctx context.Context, r io.Reader, feed func([]float32) error) error {
	block := make([]byte, blockBytes)
	samples := make([]float32, blockBytes/4)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(r, block)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("reading decoded audio: %w", err)
		}
		if n%4 != 0 {
			return errors.New("decoded audio ends with an incomplete sample")
		}
		for i := range n / 4 {
			s := math.Float32frombits(binary.LittleEndian.Uint32(block[4*i:]))
			if s != s || s > 1e6 || s < -1e6 {
				return errors.New("decoded audio contains invalid samples")
			}
			samples[i] = s
		}
		if n > 0 {
			if feedErr := feed(samples[:n/4]); feedErr != nil {
				return feedErr
			}
		}
		if err != nil {
			return nil
		}
	}
}

type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) String() string { return b.buffer.String() }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len() < stderrLimit {
		b.buffer.Write(p[:min(len(p), stderrLimit-b.buffer.Len())])
	}
	return len(p), nil
}
