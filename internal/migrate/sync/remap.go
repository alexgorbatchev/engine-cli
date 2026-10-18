package sync

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nateranda/djtools/lib"
)

const maxFileNameBytes = 255
const compactPathBytes = 255

type FileMapping struct {
	SongID      int
	SourcePath  string
	TargetPath  string
	Size        int64
	IsStreaming bool
}

// RemapLibrary produces a cloned lib.Library where song paths point to targetDriveDir,
// along with a list of FileMapping items to perform smart copying.
func RemapLibrary(sourceLib *lib.Library, targetDriveDir string) (lib.Library, []FileMapping) {
	remapped := *sourceLib

	// Copy song records; remapping only changes Path.
	remapped.Songs = make([]lib.Song, len(sourceLib.Songs))
	copy(remapped.Songs, sourceLib.Songs)

	mappings := make([]FileMapping, 0, len(sourceLib.Songs))

	for i, song := range sourceLib.Songs {
		srcPath := song.Path
		targetPath := computeTargetPath(srcPath, targetDriveDir)

		isStreaming := song.StreamingSource != "" ||
			strings.HasPrefix(song.URI, "youtube:") ||
			strings.HasPrefix(song.URI, "tidal:") ||
			strings.HasPrefix(song.URI, "soundcloud:") ||
			strings.HasPrefix(song.URI, "beatport:")

		remapped.Songs[i].Path = targetPath
		size := int64(song.Size)
		if _, info, err := resolveSource(srcPath); err == nil {
			size = info.Size()
		}

		mappings = append(mappings, FileMapping{
			SongID:      song.SongID,
			SourcePath:  srcPath,
			TargetPath:  targetPath,
			Size:        size,
			IsStreaming: isStreaming,
		})
	}

	return remapped, mappings
}

func computeTargetPath(srcPath string, targetDrive string) string {
	cleaned := filepath.Clean(srcPath)
	digest := sha256.Sum256([]byte(cleaned))
	ext := filepath.Ext(cleaned)
	name := strings.TrimSuffix(filepath.Base(cleaned), ext)
	suffix := fmt.Sprintf("-%x%s", digest[:8], ext)
	filename := truncateUTF8(name, max(1, maxFileNameBytes-len(suffix))) + suffix

	// Look for "Music" in the path to preserve subfolders
	idx := strings.Index(cleaned, "/Music/")
	var subPath string
	if idx != -1 {
		subPath = cleaned[idx+1:] // e.g., "Music/Folder/Track.mp3"
	} else {
		subPath = filepath.Base(cleaned) // fallback to filename
	}

	subPath = filepath.Join(filepath.Dir(subPath), filename)
	targetPath := filepath.Join(targetDrive, "Contents", subPath)

	// Flatten long folder hierarchies, keeping the stable source identity suffix.
	if len(targetPath) > compactPathBytes {
		// Try fallback to Contents/Music/Filename.ext
		fallback := filepath.Join(targetDrive, "Contents", "Music", filename)
		if len(fallback) <= compactPathBytes {
			return fallback
		}

		// If filename itself is too long, truncate filename (preserving extension)
		musicDir := filepath.Join(targetDrive, "Contents", "Music")
		maxFilenameLen := compactPathBytes - len(musicDir) - 1 - len(suffix)
		if maxFilenameLen > 0 {
			return filepath.Join(musicDir, truncateUTF8(name, maxFilenameLen)+suffix)
		}
		return fallback
	}

	return targetPath
}

// ValidateMappings refuses ambiguous destinations before any copying starts.
func ValidateMappings(mappings []FileMapping) error {
	seen := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		if mapping.IsStreaming {
			continue
		}
		path := strings.ToLower(filepath.Clean(mapping.TargetPath))
		if source, ok := seen[path]; ok && source != mapping.SourcePath {
			return fmt.Errorf("sources %q and %q share destination %q", source, mapping.SourcePath, mapping.TargetPath)
		}
		if len(filepath.Base(mapping.TargetPath)) > maxFileNameBytes {
			return fmt.Errorf("destination filename exceeds %d bytes: %q", maxFileNameBytes, mapping.TargetPath)
		}
		seen[path] = mapping.SourcePath
	}
	return nil
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
