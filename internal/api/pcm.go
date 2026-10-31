package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/pcm"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type pcmRequest struct {
	SampleRate int `json:"sample_rate"`
}

func readPCMRequest(w http.ResponseWriter, r *http.Request) (pcmRequest, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Use application/json.")
		return pcmRequest{}, false
	}
	var request pcmRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writePCMError(w, pcm.ErrInvalid)
		return request, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF || (request.SampleRate != 44100 && request.SampleRate != 48000) {
		writePCMError(w, pcm.ErrInvalid)
		return request, false
	}
	return request, true
}

func (s *Server) createTrackPCM(w http.ResponseWriter, r *http.Request) {
	s.createPCM(w, r, s.openTrackPCM)
}

func (s *Server) createStagedPCM(w http.ResponseWriter, r *http.Request) {
	s.createPCM(w, r, func(w http.ResponseWriter, r *http.Request) *os.File {
		file, err := staging.New(s.config.LibraryDir).Open(r.Context(), r.PathValue("id"), false)
		if err != nil {
			writeError(w, http.StatusNotFound, "staged_media_unavailable", "This staged audio is unavailable.")
			return nil
		}
		return file
	})
}

func (s *Server) openTrackPCM(w http.ResponseWriter, r *http.Request) *os.File {
	var file *os.File
	_, err := readRequestTrack(s, r, func(_ context.Context, _ *sql.DB, track *db.Track) (struct{}, error) {
		// History enrollment may discard the first observation and rerun it.
		if file != nil {
			file.Close()
			file = nil
		}
		path := track.PathString()
		if path == "" {
			return struct{}{}, &collectionReadError{http.StatusNotFound, "audio_unavailable", "This track has no local audio file."}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.config.LibraryDir, path)
		}
		var err error
		file, err = regularfile.Open(path)
		if err != nil {
			return struct{}{}, &collectionReadError{http.StatusNotFound, "audio_unavailable", "The audio file could not be opened. Check that its drive is connected."}
		}
		return struct{}{}, nil
	})
	if err != nil {
		if file != nil {
			file.Close()
		}
		writeCollectionReadError(w, err)
		return nil
	}
	return file
}

func (s *Server) createPCM(w http.ResponseWriter, r *http.Request, open func(http.ResponseWriter, *http.Request) *os.File) {
	request, ok := readPCMRequest(w, r)
	if !ok {
		return
	}
	release, ok := s.beginCollectionRead(w, r.Context())
	if !ok {
		return
	}
	file := open(w, r)
	if file != nil {
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			file = nil
			writeError(w, http.StatusNotFound, "audio_unavailable", "The audio source is not a regular file.")
		}
	}
	// Retain the opened regular file, not the database or collection read lock.
	release()
	if file == nil {
		return
	}
	metadata, err := s.pcm.Create(r.Context(), s.config.LibraryDir, file, request.SampleRate)
	if err != nil {
		writePCMError(w, err)
		return
	}
	w.Header().Set("Location", "/v1/pcm/"+metadata.ID)
	writeJSON(w, http.StatusCreated, metadata)
}

func (s *Server) pcmMetadata(w http.ResponseWriter, r *http.Request) {
	metadata, err := s.pcm.Metadata(r.Context(), s.config.LibraryDir, r.PathValue("generation"))
	if err != nil {
		writePCMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

func (s *Server) pcmWindow(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	start, startErr := strconv.ParseInt(query.Get("start_frame"), 10, 64)
	count, countErr := strconv.Atoi(query.Get("frame_count"))
	if startErr != nil || countErr != nil || len(query) != 2 || len(query["start_frame"]) != 1 || len(query["frame_count"]) != 1 {
		writePCMError(w, pcm.ErrInvalid)
		return
	}
	window, err := s.pcm.Read(r.Context(), s.config.LibraryDir, r.PathValue("generation"), start, count)
	if err != nil {
		writePCMError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(window.Data)))
	w.Header().Set("X-PCM-Generation", window.ID)
	w.Header().Set("X-PCM-Sample-Rate", strconv.Itoa(window.SampleRate))
	w.Header().Set("X-PCM-Channels", strconv.Itoa(window.Channels))
	w.Header().Set("X-PCM-Start-Frame", strconv.FormatInt(window.StartFrame, 10))
	w.Header().Set("X-PCM-Frame-Count", strconv.Itoa(window.FrameCount))
	length := "unknown"
	if window.LengthFrames != nil {
		length = strconv.FormatInt(*window.LengthFrames, 10)
	}
	w.Header().Set("X-PCM-Length-Frames", length)
	_, _ = w.Write(window.Data) // Client disconnects cannot undo a completed decode.
}

func (s *Server) releasePCM(w http.ResponseWriter, r *http.Request) {
	if err := s.pcm.Release(s.config.LibraryDir, r.PathValue("generation")); err != nil {
		writePCMError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writePCMError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusUnprocessableEntity, "pcm_decode_failed", "This audio could not be prepared for loop playback."
	switch {
	case errors.Is(err, pcm.ErrInvalid):
		status, code, message = http.StatusBadRequest, "pcm_invalid_request", "Use a supported sample rate and a bounded nonnegative frame window."
	case errors.Is(err, pcm.ErrMissing):
		status, code, message = http.StatusNotFound, "pcm_generation_unavailable", "Prepare this audio again."
	case errors.Is(err, pcm.ErrChanged):
		status, code, message = http.StatusConflict, "pcm_source_changed", "The audio changed. Prepare it again."
	case errors.Is(err, pcm.ErrBusy):
		status, code, message = http.StatusServiceUnavailable, "pcm_busy", "Audio preparation is busy. Retry when another request finishes."
	case errors.Is(err, pcm.ErrClosed):
		status, code, message = http.StatusServiceUnavailable, "pcm_unavailable", "Audio preparation is shutting down."
	case errors.Is(err, pcm.ErrUnavailable):
		status, code, message = http.StatusServiceUnavailable, "pcm_decoder_unavailable", "Install a supported FFmpeg build and retry."
	case errors.Is(err, pcm.ErrTimeline):
		code, message = "pcm_timeline_unsupported", "This audio has an ambiguous presentation timeline."
	case errors.Is(err, pcm.ErrExtentUnknown):
		status, code, message = http.StatusRequestedRangeNotSatisfiable, "pcm_extent_unknown", "Read an earlier audio window to establish the end of this source."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusRequestTimeout, "pcm_cancelled", "Audio preparation was cancelled."
	}
	writeError(w, status, code, message)
}
