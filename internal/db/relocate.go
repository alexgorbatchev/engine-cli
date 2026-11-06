package db

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// RelocateTracks replaces an exact stored path or its directory prefix. Relative
// paths remain relative to the library; they are never resolved against the host.
func RelocateTracks(ctx context.Context, database *sql.DB, oldPrefix string, newPrefix string) (int, error) {
	if database == nil {
		return 0, fmt.Errorf("database cannot be nil")
	}
	if oldPrefix == "" {
		return 0, fmt.Errorf("old prefix cannot be empty")
	}
	for _, prefix := range []string{oldPrefix, newPrefix} {
		if len(prefix) == 2 && relocationDrive(prefix) {
			return 0, fmt.Errorf("bare drive prefix %q is ambiguous; include a directory separator, such as %q", prefix, prefix+`\`)
		}
	}
	tracks, err := GetAllTracks(ctx, database)
	if err != nil {
		return 0, fmt.Errorf("fetching tracks for relocation: %w", err)
	}

	fromWindows := relocationWindowsPath(oldPrefix)
	toWindows := relocationWindowsPath(newPrefix)
	if !strings.ContainsAny(newPrefix, `/\:`) {
		toWindows = fromWindows
	}
	from := relocationDirectory(relocationSlashes(oldPrefix, fromWindows))
	to := relocationDirectory(relocationSlashes(newPrefix, toWindows))
	separator := "/"
	if toWindows && !strings.Contains(newPrefix, "/") {
		separator = `\`
	}

	count := 0
	for _, t := range tracks {
		stored := relocationSlashes(t.PathString(), fromWindows)
		target := to
		if stored != from {
			suffix, ok := strings.CutPrefix(stored, strings.TrimSuffix(from, "/")+"/")
			if !ok {
				continue
			}
			if toWindows && !fromWindows && strings.Contains(suffix, `\`) {
				return count, fmt.Errorf("relocating track %d: a literal backslash in the source filename cannot become a Windows separator", t.ID)
			}
			target = strings.TrimSuffix(to, "/") + "/" + suffix
		}
		filenamePath := target
		if toWindows && relocationDrive(filenamePath) {
			filenamePath = filenamePath[2:]
		}
		t.Filename = sql.NullString{String: path.Base(filenamePath), Valid: true}
		t.Path = sql.NullString{String: strings.ReplaceAll(target, "/", separator), Valid: true}
		if err := UpdateTrackMetadata(ctx, database, &t); err != nil {
			return count, fmt.Errorf("updating relocated track %d: %w", t.ID, err)
		}
		count++
	}
	return count, nil
}

func relocationWindowsPath(value string) bool {
	if relocationDrive(value) || strings.HasPrefix(value, `\`) {
		return true
	}
	if strings.HasPrefix(value, "/") {
		return false
	}
	// An explicit backslash-only selector permits cross-host relative Windows
	// paths. Bare relative selectors retain the host's filename semantics.
	return filepath.Separator == '\\' || strings.Contains(value, `\`) && !strings.Contains(value, "/")
}

func relocationDrive(value string) bool {
	return len(value) >= 2 && value[1] == ':' &&
		(value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z')
}

func relocationSlashes(value string, windows bool) string {
	if windows {
		return strings.ReplaceAll(value, `\`, "/")
	}
	return value
}

func relocationDirectory(value string) string {
	trimmed := strings.TrimRight(value, "/")
	if trimmed == "" {
		return value
	}
	// D:/ is a drive root; D: is drive-relative. Never collapse that distinction.
	if len(trimmed) == 2 && relocationDrive(trimmed) && trimmed != value {
		return trimmed + "/"
	}
	return trimmed
}
