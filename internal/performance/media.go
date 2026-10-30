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
	"github.com/nateranda/djtools/engine"
)

type mediaSource struct {
	path string
	info os.FileInfo
}

func (m *mediaSource) check() error {
	info, err := os.Stat(m.path)
	if err != nil {
		return failure("cue_media_changed", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(m.info, info) || info.Size() != m.info.Size() || !info.ModTime().Equal(m.info.ModTime()) {
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

// Inspection records unavailable media explicitly; reading and clearing saved cues never require online audio.
func inspectMedia(library, path string) string {
	path, err := filepath.EvalSymlinks(mediaPath(library, path))
	if errors.Is(err, os.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		return "unavailable"
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		return "unavailable"
	}
	if !info.Mode().IsRegular() {
		return "not-regular"
	}
	id, err := mediaIdentity(path, info)
	if err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%q:%s:%d:%d", path, id, info.Size(), info.ModTime().UnixNano())
}

func sampleExtent(ctx context.Context, library string, s snapshot) (float64, float64, *mediaSource, error) {
	if rate, limit, ok := nativeExtent(s.Track); ok {
		return rate, limit, nil, nil
	}
	if inspectMedia(library, s.Path) != s.Media {
		return 0, 0, nil, stale()
	}
	path := mediaPath(library, s.Path)
	path, err := filepath.EvalSymlinks(path)
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
	m := &mediaSource{path: path, info: info}
	rate, limit, err := probeExtent(ctx, path)
	if err != nil {
		return 0, 0, nil, err
	}
	if err := m.check(); err != nil {
		return 0, 0, nil, err
	}
	if inspectMedia(library, s.Path) != s.Media {
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
