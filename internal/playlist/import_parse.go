package playlist

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"golang.org/x/text/encoding/charmap"
)

const maxImportBytes = 10 * 1024 * 1024
const maxImportLine = 64 * 1024
const maxImportEntries = 10000

type importSource struct {
	path    string
	bytes   []byte
	entries []ImportEntry
}

func importError(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}

func readImport(ctx context.Context, opts ImportOptions) (*importSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".m3u" && ext != ".m3u8" {
		return nil, importError("playlist_import_format", "choose an M3U or M3U8 file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, &progress.Error{Code: "playlist_import_unavailable", Cause: err}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxImportBytes {
		return nil, importError("playlist_import_format", "playlist must be a regular file no larger than 10 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxImportBytes {
		return nil, importError("playlist_import_format", "playlist exceeds 10 MiB")
	}
	encoding := opts.Encoding
	if encoding == "" {
		encoding = importUTF8
	}
	text := bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if ext == ".m3u8" && encoding != importUTF8 {
		return nil, importError("playlist_import_encoding", "M3U8 requires UTF-8")
	}
	switch encoding {
	case importUTF8:
		if !utf8.Valid(text) {
			return nil, importError("playlist_import_encoding", "playlist is not valid UTF-8; choose its encoding explicitly")
		}
	case importWindows1252, importLatin1:
		decoder := charmap.Windows1252.NewDecoder()
		if encoding == importLatin1 {
			decoder = charmap.ISO8859_1.NewDecoder()
		}
		text, err = decoder.Bytes(text)
		if err != nil {
			return nil, &progress.Error{Code: "playlist_import_encoding", Cause: err}
		}
	default:
		return nil, importError("playlist_import_encoding", "unsupported playlist encoding")
	}
	source := &importSource{path: path, bytes: raw, entries: []ImportEntry{}}
	scanner := bufio.NewScanner(bytes.NewReader(text))
	scanner.Buffer(make([]byte, 4096), maxImportLine)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(entry, "#EXT-X-") {
			return nil, importError("playlist_import_format", "HLS streaming playlists are not local music playlists")
		}
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		if len(source.entries) >= maxImportEntries || strings.ContainsFunc(entry, unicode.IsControl) {
			return nil, importError("playlist_import_format", "playlist contains invalid entries or exceeds 10000 entries")
		}
		resolved, err := importPath(filepath.Dir(path), entry)
		if err != nil {
			return nil, err
		}
		source.entries = append(source.entries, ImportEntry{Line: line, Path: resolved, Status: importUnregistered})
	}
	if err := scanner.Err(); err != nil {
		return nil, &progress.Error{Code: "playlist_import_format", Cause: err}
	}
	if len(source.entries) == 0 {
		return nil, importError("playlist_import_empty", "playlist contains no music paths")
	}
	return source, nil
}

func importPath(base, entry string) (string, error) {
	// A Windows path is not a URI scheme. Foreign host paths cannot be resolved locally.
	if len(entry) > 1 && entry[1] == ':' {
		if runtime.GOOS != "windows" {
			return "", importError("playlist_import_format", "Windows paths require a Windows server")
		}
	} else {
		var err error
		entry, err = importURIPath(entry)
		if err != nil {
			return "", err
		}
	}
	if runtime.GOOS != "windows" && strings.Contains(entry, "\\") {
		return "", importError("playlist_import_format", "Windows path separators require a Windows server")
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(base, entry)
	}
	return filepath.Clean(entry), nil
}

func importURIPath(entry string) (string, error) {
	// Explicit paths preserve POSIX filenames containing colons or literal percent signs.
	if filepath.IsAbs(entry) || strings.HasPrefix(entry, "./") || strings.HasPrefix(entry, "../") {
		return entry, nil
	}
	u, err := url.Parse(entry)
	if err != nil {
		if !strings.Contains(entry, ":") {
			return entry, nil
		}
		return "", importError("playlist_import_format", "playlist path or URI is malformed")
	}
	if u.Scheme == "" {
		return entry, nil
	}
	if !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return "", importError("playlist_import_format", "only local paths and local file URIs are supported")
	}
	entry = u.Path
	if strings.ContainsFunc(entry, unicode.IsControl) {
		return "", importError("playlist_import_format", "playlist paths cannot contain control characters")
	}
	if runtime.GOOS == "windows" && len(entry) > 2 && entry[0] == '/' && entry[2] == ':' {
		entry = entry[1:]
	}
	return entry, nil
}
