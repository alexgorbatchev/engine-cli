package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"path/filepath"
	"time"

	"github.com/dj/engine-cli/internal/db"
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
	var data []byte
	err = d.QueryRowContext(r.Context(), "SELECT albumArt FROM AlbumArt WHERE id = ?", t.AlbumArtID).Scan(&data)
	if err == sql.ErrNoRows || err == nil && len(data) == 0 {
		writeError(w, http.StatusNotFound, "artwork_unavailable", "This track has no cover artwork.")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "artwork_unavailable", "Could not read this track's cover artwork.")
		return
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" && contentType != "image/gif" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_artwork", "This artwork format cannot be displayed.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "artwork", time.Time{}, bytes.NewReader(data))
}
