package fingerprint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const stderrLimit = 1024
const nativeResampler = "aresample=11025:resampler=swr:filter_size=16:phase_shift=8:linear_interp=1:cutoff=0.8"

type File struct {
	SHA256 string `json:"sha256"`
	Audio
}

// Analyze checks content before and after decoding so concurrent edits cannot
// publish a fingerprint under the wrong content identity.
func Analyze(ctx context.Context, path string) (File, error) {
	hash, err := FileHash(ctx, path)
	if err != nil {
		return File{}, err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0", "-ac", "1", "-af", nativeResampler, "-ar", "11025", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	a, err := decodeCommand(ctx, cmd)
	if err != nil {
		return File{}, err
	}
	after, err := FileHash(ctx, path)
	if err != nil {
		return File{}, err
	}
	if after != hash {
		return File{}, fmt.Errorf("audio changed while fingerprinting; retry after the file is no longer being edited")
	}
	return File{SHA256: hash, Audio: a}, nil
}

func decodeCommand(ctx context.Context, cmd *exec.Cmd) (Audio, error) {
	var stderr limitedOutput
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Audio{}, err
	}
	if err = cmd.Start(); err != nil {
		return Audio{}, fmt.Errorf("starting audio decoder; install FFmpeg and retry: %w", err)
	}
	a, readErr := readPCM(ctx, stdout)
	if readErr != nil {
		_ = cmd.Process.Kill() // Stop the producer before waiting after a read failure.
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Audio{}, ctx.Err()
	}
	if waitErr != nil {
		return Audio{}, fmt.Errorf("decoding audio: %s: %w", progress.PublicError(errors.New(strings.TrimSpace(stderr.String()))), errors.Join(readErr, waitErr))
	}
	return a, readErr
}

type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) String() string { return b.buffer.String() }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.buffer.Len() < stderrLimit {
		if _, err := b.buffer.Write(p[:min(len(p), stderrLimit-b.buffer.Len())]); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// FileHash identifies bytes independently of paths and metadata timestamps.
func FileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("audio is not a regular file")
	}
	h := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		_, err := io.CopyN(h, f, 128*1024)
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}
