package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/artworkcache"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func (s *Server) trackArtwork(w http.ResponseWriter, r *http.Request) {
	t := s.requestTrack(w, r)
	if t == nil {
		return
	}
	d, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not open the library artwork.")
		return
	}
	defer d.Close()
	var data, hash []byte
	err = d.QueryRowContext(r.Context(), "SELECT albumArt, hash FROM AlbumArt WHERE id = ?", t.AlbumArtID).Scan(&data, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "artwork_unavailable", "Could not read this track's cover artwork.")
		return
	}
	if len(data) == 0 {
		data, err = s.cachedArtwork(hash, []byte(t.AlbumArtSourceHash.String))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "artwork_unavailable", "Could not read this track's cached cover artwork.")
			return
		}
	}
	if r.URL.Query().Get("size") == "full" && len(data) > 0 {
		full, cacheErr := s.cachedArtwork(hash, []byte(t.AlbumArtSourceHash.String))
		if cacheErr == nil && len(full) > 0 {
			data = full
		}
	}
	if len(data) == 0 {
		writeError(w, http.StatusNotFound, "artwork_unavailable", "This track has no cover artwork.")
		return
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" && contentType != "image/gif" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_artwork", "This artwork format cannot be displayed.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	serveArtworkContent(w, r, data)
}

func serveArtworkContent(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("Cache-Control", "private, no-cache")
	digest := sha256.Sum256(data)
	w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
	http.ServeContent(w, r, "artwork", time.Time{}, bytes.NewReader(data))
}

// Engine stores cache-backed SHA-1 hashes as binary BLOBs as well as hexadecimal
// text. Generate filenames from decoded hashes, never from database paths.
func (s *Server) cachedArtwork(hashes ...[]byte) ([]byte, error) {
	for _, path := range artworkcache.Paths(s.config.LibraryDir, hashes...) {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(data) > 0 {
			return data, nil
		}
	}
	return nil, nil
}
