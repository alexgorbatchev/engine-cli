package history

import (
	"os"
	"path/filepath"
	"strings"
)

// audioExtensions are the audio formats Engine DJ plays.
var audioExtensions = map[string]bool{
	".aac": true, ".aif": true, ".aiff": true, ".alac": true, ".flac": true, ".m4a": true,
	".mp3": true, ".mp4": true, ".ogg": true, ".opus": true, ".wav": true, ".wma": true,
}

// KeptAudio reports the audio files history keeps copies of so Undo can put
// them back, such as originals rewritten by tag writes or removed with their
// tracks, and the space those copies take. Each stored copy counts once.
type KeptAudio struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// ListKeptAudio totals the audio copies every history entry still references.
func ListKeptAudio(lib string) (KeptAudio, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return KeptAudio{}, err
	}
	list, err := List(lib)
	if err != nil {
		return KeptAudio{}, err
	}
	seen := make(map[string]bool)
	var kept KeptAudio
	for _, e := range list {
		if e.Before == nil {
			continue
		}
		for path, state := range e.Before.Files {
			if state.Hash == "" || seen[state.Hash] || !audioExtensions[strings.ToLower(filepath.Ext(path))] {
				continue
			}
			info, err := os.Stat(filepath.Join(dir, "objects", state.Hash))
			if err != nil {
				continue
			}
			seen[state.Hash] = true
			kept.Files++
			kept.Bytes += info.Size()
		}
	}
	return kept, nil
}
