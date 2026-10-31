package performance

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/audiodecode"
	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/nateranda/djtools/engine"
)

type mediaSource struct {
	path     string
	info     os.FileInfo
	identity string
}

func (m *mediaSource) check() error {
	info, err := os.Stat(m.path)
	if err != nil {
		return failure("cue_media_changed", err)
	}
	identity, identityErr := fileidentity.Identity(m.path, info)
	if identityErr != nil {
		return failure("cue_media_changed", identityErr)
	}
	if !info.Mode().IsRegular() || identity != m.identity || info.Size() != m.info.Size() || !info.ModTime().Equal(m.info.ModTime()) {
		return failure("cue_media_changed", errors.New("track audio changed while preparing its cue; reload and retry"))
	}
	return nil
}

func nativeExtent(blob []byte) (float64, float64, bool) {
	if len(blob) > 0 {
		data, err := engine.QUncompress(blob)
		if err == nil && len(data) >= 16 {
			rate := math.Float64frombits(binary.BigEndian.Uint64(data[:8]))
			count := binary.BigEndian.Uint64(data[8:16])
			if finite(rate) && rate > 0 && count > 0 && count <= 1<<53 {
				return rate, float64(count), true
			}
		}
	}
	return 0, 0, false
}

func mediaPath(library, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(library, path)
	}
	return path
}

// Inspection records missing media explicitly; unobservable identities cannot establish a revision.
func inspectMedia(library, path string) (string, error) {
	path, err := filepath.EvalSymlinks(mediaPath(library, path))
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", failure("cue_media_unavailable", err)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", failure("cue_media_unavailable", err)
	}
	if !info.Mode().IsRegular() {
		return "not-regular", nil
	}
	id, err := fileidentity.Identity(path, info)
	if err != nil {
		return "", failure("cue_media_unavailable", err)
	}
	return fmt.Sprintf("%q:%s:%d:%d", path, id, info.Size(), info.ModTime().UnixNano()), nil
}

func sampleExtent(ctx context.Context, library string, s snapshot) (float64, float64, *mediaSource, error) {
	if rate, limit, ok := nativeExtent(s.Track); ok {
		return rate, limit, nil, nil
	}
	observed, err := inspectMedia(library, s.Path)
	if err != nil {
		return 0, 0, nil, err
	}
	if observed != s.Media {
		return 0, 0, nil, stale()
	}
	path := mediaPath(library, s.Path)
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return 0, 0, nil, failure("cue_media_unavailable", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, nil, failure("cue_media_unavailable", err)
	}
	if !info.Mode().IsRegular() {
		return 0, 0, nil, failure("cue_media_unavailable", errors.New("track audio is not a regular file"))
	}
	identity, err := fileidentity.Identity(path, info)
	if err != nil {
		return 0, 0, nil, failure("cue_media_unavailable", err)
	}
	m := &mediaSource{path: path, info: info, identity: identity}
	rate, limit, err := probeExtent(ctx, path)
	if err != nil {
		return 0, 0, nil, err
	}
	if err := m.check(); err != nil {
		return 0, 0, nil, err
	}
	observed, err = inspectMedia(library, s.Path)
	if err != nil {
		return 0, 0, nil, err
	}
	if observed != s.Media {
		return 0, 0, nil, stale()
	}
	return rate, limit, m, nil
}

func probeExtent(ctx context.Context, path string) (float64, float64, error) {
	rate, samples, err := audiodecode.NativeExtent(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		return 0, 0, failure("cue_media_unavailable", err)
	}
	return rate, float64(samples), nil
}
