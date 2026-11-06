package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/artworkcache"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func (s *Server) trackArtwork(w http.ResponseWriter, r *http.Request) {
	data, err := readRequestTrack(s, r, func(ctx context.Context, d *sql.DB, t *db.Track) ([]byte, error) {
		var data, hash []byte
		err := d.QueryRowContext(ctx, "SELECT albumArt, hash FROM AlbumArt WHERE id = ?", t.AlbumArtID).Scan(&data, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return nil, &collectionReadError{http.StatusServiceUnavailable, "artwork_unavailable", "Could not read this track's cover artwork."}
		}
		if len(data) == 0 {
			data, err = s.cachedArtwork(hash, []byte(t.AlbumArtSourceHash.String))
			if err != nil {
				return nil, &collectionReadError{http.StatusServiceUnavailable, "artwork_unavailable", "Could not read this track's cached cover artwork."}
			}
		}
		if r.URL.Query().Get("size") == "full" && len(data) > 0 {
			full, cacheErr := s.cachedArtwork(hash, []byte(t.AlbumArtSourceHash.String))
			if cacheErr == nil && len(full) > 0 {
				data = full
			}
		}
		if len(data) == 0 {
			return nil, &collectionReadError{http.StatusNotFound, "artwork_unavailable", "This track has no cover artwork."}
		}
		return data, nil
	})
	if err != nil {
		writeCollectionReadError(w, err)
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
