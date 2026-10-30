package playlist

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

func normalizeExport(opts ExportOptions) (ExportOptions, error) {
	if opts.Path == "" {
		return opts, playlistFileError("playlist_export_target", "choose a destination file")
	}
	if !safeExportPath(opts.Path) {
		return opts, playlistFileError("playlist_export_format", "destination path cannot be represented safely")
	}
	path, err := filepath.Abs(opts.Path)
	if err != nil {
		return opts, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".m3u" && ext != ".m3u8" {
		return opts, playlistFileError("playlist_export_format", "choose an M3U or M3U8 destination")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return opts, playlistFileError("playlist_export_target", "destination parent must already exist and be accessible")
	}
	opts.Path = filepath.Join(parent, filepath.Base(path))
	if _, err := os.Lstat(opts.Path); err == nil {
		return opts, playlistFileError("playlist_export_exists", "destination already exists; choose a new filename")
	} else if !os.IsNotExist(err) {
		return opts, playlistFileError("playlist_export_target", "destination cannot be inspected")
	}
	return normalizeExportFormat(opts, ext)
}

func normalizeExportFormat(opts ExportOptions, ext string) (ExportOptions, error) {
	if opts.Encoding == "" {
		opts.Encoding = m3uUTF8
	}
	if opts.Encoding != m3uUTF8 && opts.Encoding != m3uWindows1252 && opts.Encoding != m3uLatin1 {
		return opts, playlistFileError("playlist_export_encoding", "unsupported playlist encoding")
	}
	if ext == ".m3u8" && opts.Encoding != m3uUTF8 {
		return opts, playlistFileError("playlist_export_encoding", "M3U8 requires UTF-8")
	}
	if opts.PathMode == "" {
		opts.PathMode = exportAbsolute
	}
	if opts.PathMode != exportAbsolute && opts.PathMode != exportRelative {
		return opts, playlistFileError("playlist_export_format", "choose absolute or relative paths")
	}
	return opts, nil
}

func prepareExportEntry(root string, opts ExportOptions, member exportMember) (ExportEntry, error) {
	entry := ExportEntry{EntityID: member.EntityID, TrackID: member.TrackID.Int64, Title: exportTitle(member), Duration: member.Duration.Int64, Status: exportUnavailable}
	if entry.Duration < 0 || !member.Duration.Valid {
		entry.Duration = -1
	}
	if !member.TrackID.Valid || !member.Path.Valid || member.Path.String == "" {
		return entry, nil
	}
	path, err := importPath(root, member.Path.String)
	if err != nil {
		return entry, nil
	}
	if !safeExportPath(path) {
		return entry, playlistFileError("playlist_export_format", "media path cannot be represented safely in M3U")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || (member.Available.Valid && !member.Available.Bool) {
		entry.Path = path
		return entry, nil
	}
	if opts.PathMode == exportRelative {
		path, err = filepath.Rel(filepath.Dir(opts.Path), path)
		if err != nil {
			return entry, playlistFileError("playlist_export_format", "media cannot be represented relative to this destination volume")
		}
	}
	path = filepath.ToSlash(path)
	if !safeExportPath(path) {
		return entry, playlistFileError("playlist_export_format", "media path cannot be represented safely in M3U")
	}
	if opts.PathMode == exportRelative {
		path = explicitExportPath(path)
	}
	entry.Path, entry.Status = path, exportReady
	return entry, nil
}

func safeExportPath(path string) bool {
	return utf8.ValidString(path) && !strings.ContainsFunc(path, unicode.IsControl) && strings.TrimSpace(path) == path && (runtime.GOOS == "windows" || !strings.Contains(path, "\\"))
}

func explicitExportPath(path string) string {
	u, err := url.Parse(path)
	if strings.HasPrefix(path, "#") || strings.HasPrefix(path, " ") || err != nil || u.Scheme != "" {
		return "./" + path
	}
	return path
}

func exportTitle(member exportMember) string {
	title, artist := exportMetadata(member.Title.String), exportMetadata(member.Artist.String)
	if title == "" {
		title = exportMetadata(filepath.Base(member.Path.String))
		if title == "" || title == "." {
			title = "Untitled track"
		}
	}
	if artist != "" {
		return artist + " - " + title
	}
	return title
}

func exportMetadata(value string) string {
	// Metadata is one EXTINF field; control bytes cannot introduce playlist records.
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " "))
}

func serializeExport(plan *ExportPlan) ([]byte, error) {
	var output strings.Builder
	output.WriteString("#EXTM3U\n")
	for _, entry := range plan.Entries {
		if entry.Status != exportReady {
			continue
		}
		fmt.Fprintf(&output, "#EXTINF:%d,%s\n%s\n", entry.Duration, entry.Title, entry.Path)
	}
	raw := []byte(output.String())
	if !utf8.Valid(raw) {
		return nil, playlistFileError("playlist_export_encoding", "metadata is not valid UTF-8")
	}
	if plan.Encoding == m3uUTF8 {
		return raw, nil
	}
	encoder := charmap.Windows1252.NewEncoder()
	if plan.Encoding == m3uLatin1 {
		encoder = charmap.ISO8859_1.NewEncoder()
	}
	encoded, err := encoder.Bytes(raw)
	if err != nil {
		return nil, playlistFileError("playlist_export_encoding", "playlist contains characters unavailable in the chosen encoding; use UTF-8")
	}
	return encoded, nil
}
