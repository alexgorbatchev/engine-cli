package api

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
)

func (s *Server) requestTrack(w http.ResponseWriter, r *http.Request) *db.Track {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if (err != nil || id <= 0) && !strings.HasPrefix(r.PathValue("id"), "eng:") {
		writeError(w, http.StatusBadRequest, "invalid_track", "Use a positive numeric track ID.")
		return nil
	}
	database, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not open the library. Check the configured library folder.")
		return nil
	}
	defer database.Close()
	track, err := db.FindTrack(r.Context(), database, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not read this track from the library.")
		return nil
	}
	if track == nil {
		writeError(w, http.StatusNotFound, "track_not_found", "This track is no longer in the library.")
	}
	return track
}

func (s *Server) trackMetadata(w http.ResponseWriter, r *http.Request) {
	track := s.requestTrack(w, r)
	if track == nil {
		return
	}
	database, err := db.Open(filepath.Join(s.config.LibraryDir, "Database2", "m.db"), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not open the library. Check the configured library folder.")
		return
	}
	defer database.Close()
	history, err := s.playHistory(r.Context(), database)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not read this track from the library.")
		return
	}
	result, err := details.OneTrack(r.Context(), database, history, track)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "library_unavailable", "Could not read this track's playlists from the library.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) trackAudio(w http.ResponseWriter, r *http.Request) {
	release, ok := s.beginCollectionRead(w, r.Context())
	if !ok {
		return
	}
	defer release()
	// Cross-origin media in no-cors mode omits Origin. Require CORS mode so the
	// server's exact browser-origin allowlist also protects audio element loads.
	if r.Header.Get("Sec-Fetch-Dest") == "audio" && r.Header.Get("Sec-Fetch-Site") != "same-origin" && r.Header.Get("Origin") == "" {
		writeError(w, http.StatusForbidden, "origin_required", "Audio previews require an allowed browser origin.")
		return
	}
	track := s.requestTrack(w, r)
	if track == nil {
		return
	}
	path := track.PathString()
	if path == "" {
		writeError(w, http.StatusNotFound, "audio_unavailable", "This track has no local audio file.")
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.config.LibraryDir, path)
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	// M4A is an MP4 audio container (RFC 4337), not the LATM transport that
	// some system MIME databases incorrectly associate with this extension.
	if strings.EqualFold(filepath.Ext(path), ".m4a") {
		contentType = "audio/mp4"
	}
	if !strings.HasPrefix(contentType, "audio/") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_audio", "This file type is not available for browser playback.")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "audio_unavailable", "The audio file could not be opened. Check that its drive is connected.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "audio_unavailable", "The track path is not an audio file.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	// Private clients may reuse audio-derived waveforms after checking the file validators.
	w.Header().Set("Cache-Control", audioCacheControl)
	release()
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
