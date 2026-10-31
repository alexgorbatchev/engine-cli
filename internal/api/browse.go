package api

import (
	"context"
	"database/sql"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
)

func (s *Server) trackMetadata(w http.ResponseWriter, r *http.Request) {
	result, err := readRequestTrack(s, r, func(ctx context.Context, database *sql.DB, track *db.Track) (any, error) {
		plays, err := s.playHistory(ctx, database)
		if err != nil {
			return nil, err
		}
		return details.OneTrack(ctx, database, plays, track)
	})
	if err != nil {
		writeCollectionReadError(w, err)
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
	var file *os.File
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	prepared, err := readRequestTrack(s, r, func(ctx context.Context, _ *sql.DB, track *db.Track) (*preparedAudio, error) {
		// An optimistic observation may be retried after history enrollment.
		if file != nil {
			file.Close()
			file = nil
		}
		path := track.PathString()
		if path == "" {
			return nil, &collectionReadError{http.StatusNotFound, "audio_unavailable", "This track has no local audio file."}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.config.LibraryDir, path)
		}
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		// M4A is an MP4 audio container, not the LATM transport reported by some MIME databases.
		if strings.EqualFold(filepath.Ext(path), ".m4a") {
			contentType = "audio/mp4"
		}
		if !strings.HasPrefix(contentType, "audio/") {
			return nil, &collectionReadError{http.StatusUnsupportedMediaType, "unsupported_audio", "This file type is not available for browser playback."}
		}
		var err error
		file, err = regularfile.Open(path)
		if err != nil {
			return nil, &collectionReadError{http.StatusNotFound, "audio_unavailable", "The audio file could not be opened. Check that its drive is connected."}
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, &collectionReadError{http.StatusNotFound, "audio_unavailable", "The track path is not an audio file."}
		}
		return &preparedAudio{info: info, contentType: contentType}, nil
	})
	if err != nil {
		writeCollectionReadError(w, err)
		return
	}
	w.Header().Set("Content-Type", prepared.contentType)
	// Private clients may reuse audio-derived waveforms after checking the file validators.
	w.Header().Set("Cache-Control", audioCacheControl)
	release()
	http.ServeContent(w, r, prepared.info.Name(), prepared.info.ModTime(), file)
}

type preparedAudio struct {
	info        os.FileInfo
	contentType string
}
