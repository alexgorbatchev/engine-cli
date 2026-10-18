package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const copyBufferSize = 1024 * 1024

type ProgressCallback func(copiedBytes, totalBytes int64)
type Copier struct{}

func NewCopier() *Copier { return &Copier{} }

func resolveSource(src string) (string, os.FileInfo, error) {
	info, err := os.Stat(src)
	if os.IsNotExist(err) && !filepath.IsAbs(src) {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", nil, homeErr
		}
		src = filepath.Join(home, src)
		info, err = os.Stat(src)
	}
	if err != nil {
		return "", nil, fmt.Errorf("inspecting source %q: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("source %q is not a regular file", src)
	}
	return src, info, nil
}

func fileDigest(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() // Read-only descriptor.
	hash := sha256.New()
	buf := make([]byte, copyBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		} // hash.Hash.Write never returns an error.
		if errors.Is(err, io.EOF) {
			return hash.Sum(nil), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func matchingFile(ctx context.Context, src, dst string, size int64) (bool, error) {
	info, err := os.Stat(dst)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("destination %q is not a regular file", dst)
	}
	if info.Size() != size {
		return false, nil
	}
	a, err := fileDigest(ctx, src)
	if err != nil {
		return false, err
	}
	b, err := fileDigest(ctx, dst)
	if err != nil {
		return false, err
	}
	return bytes.Equal(a, b), nil
}

// InspectTrack distinguishes unavailable audio from verified destination files.
func InspectTrack(ctx context.Context, mapping FileMapping) (Status, error) {
	if err := ctx.Err(); err != nil {
		return StatusPending, err
	}
	if mapping.IsStreaming {
		return StatusUnavailable, nil
	}
	src, info, err := resolveSource(mapping.SourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return StatusUnavailable, nil
	}
	if err != nil {
		return StatusFailed, err
	}
	same, err := matchingFile(ctx, src, mapping.TargetPath, info.Size())
	if err != nil {
		return StatusFailed, err
	}
	if same {
		return StatusSkipped, nil
	}
	return StatusPending, nil
}

// CopyFile commits a complete, flushed file with an atomic rename.
func (c *Copier) CopyFile(ctx context.Context, src, dst string, progress ProgressCallback) (copied, skipped bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	src, info, err := resolveSource(src)
	if err != nil {
		return false, false, err
	}
	same, err := matchingFile(ctx, src, dst, info.Size())
	if err != nil {
		return false, false, err
	}
	if same {
		if progress != nil {
			progress(info.Size(), info.Size())
		}
		return false, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, false, fmt.Errorf("creating destination directory: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return false, false, err
	}
	defer in.Close() // Read-only descriptor.
	out, err := os.CreateTemp(filepath.Dir(dst), ".audio-*.tmp")
	if err != nil {
		return false, false, err
	}
	temp := out.Name()
	defer func() {
		out.Close()     // Already closed on success; best-effort cleanup on failure.
		os.Remove(temp) // Successful rename already removed this name.
	}()
	var written int64
	buf := make([]byte, copyBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return false, false, err
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			count, writeErr := out.Write(buf[:n])
			if writeErr != nil {
				return false, false, fmt.Errorf("writing %q: %w", dst, writeErr)
			}
			written += int64(count)
			if progress != nil {
				progress(written, info.Size())
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false, false, readErr
		}
	}
	latest, err := in.Stat()
	if err != nil {
		return false, false, err
	}
	if written != info.Size() || latest.Size() != info.Size() || !latest.ModTime().Equal(info.ModTime()) {
		return false, false, fmt.Errorf("source changed while copying %q", src)
	}
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if err := out.Chmod(0644); err != nil {
		return false, false, err
	}
	if err := out.Sync(); err != nil {
		return false, false, fmt.Errorf("flushing %q: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return false, false, err
	}
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if err := os.Rename(temp, dst); err != nil {
		return false, false, err
	}
	return true, false, nil
}
