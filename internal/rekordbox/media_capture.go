package rekordbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/audiodecode"
	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
)

const mediaCode = "rekordbox_import_media"

func observeMedia(path string) (mediaFile, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return mediaFile{}, fmt.Errorf("resolving import media: %w", err)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return mediaFile{path: path, missing: true}, nil
	}
	if err != nil {
		return mediaFile{}, fmt.Errorf("observing import media: %w", err)
	}
	if !info.Mode().IsRegular() {
		return mediaFile{}, importError(mediaCode, "import media is not a regular file")
	}
	key, err := fileidentity.Identity(path, info)
	if err != nil {
		return mediaFile{}, fmt.Errorf("capturing import media identity: %w", err)
	}
	return mediaFile{path: path, key: key, size: info.Size(), modified: info.ModTime().UTC()}, nil
}

func (m mediaFile) check() error {
	current, err := observeMedia(m.path)
	if err != nil {
		return err
	}
	if current.key != m.key || current.missing != m.missing || current.size != m.size || !current.modified.Equal(m.modified) {
		return importError(staleCode, "reviewed import media changed; review again")
	}
	return nil
}

func captureMedia(ctx context.Context, src source, destination destinationObservation, library string, document mediaFile) (mediaObservation, error) {
	files := mediaObservation{document: document, source: make(map[string]mediaFile, len(src.selectors)), native: make(map[int64]mediaFile, len(destination.tracks))}
	for i, selector := range src.selectors {
		if err := ctx.Err(); err != nil {
			return mediaObservation{}, err
		}
		file, err := observeMedia(src.library.Songs[i].Path)
		if err != nil {
			return mediaObservation{}, err
		}
		files.source[selector] = file
	}
	for _, track := range destination.tracks {
		if err := ctx.Err(); err != nil {
			return mediaObservation{}, err
		}
		path := track.path
		if !filepath.IsAbs(path) {
			path = filepath.Join(library, path)
		}
		file, err := observeMedia(path)
		if err != nil {
			return mediaObservation{}, err
		}
		files.native[track.id] = file
	}
	return files, nil
}

func (m mediaObservation) check(ctx context.Context) error {
	if err := m.document.check(); err != nil {
		return err
	}
	for _, file := range m.source {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := file.check(); err != nil {
			return err
		}
	}
	for _, file := range m.native {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := file.check(); err != nil {
			return err
		}
	}
	return nil
}

// Only newly inserted tracks need decoding. Existing tracks retain their native
// performance data, while every observed file remains bound to review freshness.
func decodeNewMedia(ctx context.Context, entries []Entry, files mediaObservation) (map[string]audioExtent, error) {
	extents := make(map[string]audioExtent)
	for _, entry := range entries {
		if entry.Status != statusNew {
			continue
		}
		file := files.source[entry.SourceID]
		if err := file.check(); err != nil {
			return nil, err
		}
		rate, frames, err := audiodecode.NativeExtent(ctx, file.path)
		if err != nil {
			return nil, fmt.Errorf("decoding import media extent: %w", err)
		}
		if err := file.check(); err != nil {
			return nil, err
		}
		extents[entry.SourceID] = audioExtent{rate: rate, frames: frames, bytes: file.size}
	}
	return extents, nil
}
