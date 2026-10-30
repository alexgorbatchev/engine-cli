package analysis

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

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const (
	stderrLimit = 1024
	blockBytes  = 64 * 1024
)

// decode streams the first audio stream as mono 32-bit float samples at SampleRate.
func decode(ctx context.Context, path string, feed func([]float32) error) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
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
