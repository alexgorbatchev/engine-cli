package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

var errArtworkUploadTooLarge = errors.New("choose an image smaller than 8 MB")

func (s *Server) editTrackUpload(w http.ResponseWriter, r *http.Request) {
	id, err := readRequestTrack(s, r, func(_ context.Context, _ *sql.DB, track *db.Track) (int64, error) {
		return track.ID, nil
	})
	if err != nil {
		writeCollectionReadError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, artwork.MaxUploadBytes+maxRequestBytes)
	p, err := readArtworkUpload(r)
	if err != nil {
		status := http.StatusBadRequest
		var limitError *http.MaxBytesError
		if errors.Is(err, errArtworkUploadTooLarge) || errors.As(err, &limitError) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, "invalid_artwork", err.Error())
		return
	}
	if p.Identifier != "" && p.Identifier != r.PathValue("id") && p.Identifier != strconv.FormatInt(id, 10) {
		writeError(w, http.StatusBadRequest, "invalid_track", "The uploaded edit identifies a different track.")
		return
	}
	p.Identifier = strconv.FormatInt(id, 10)
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameters", err.Error())
		return
	}
	bound := &boundOperation{run: func(ctx context.Context) (any, error) {
		return s.database(ctx, func(d *sql.DB) (any, error) {
			id, err := track.EditMetadata(ctx, d, p, s.config.ArtworkRunner)
			if err != nil {
				return nil, err
			}
			return changedTrack{TrackID: id}, nil
		})
	}}
	job, err := s.start(s.operations["track.update"], bound)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func readArtworkUpload(r *http.Request) (track.MetadataEdit, error) {
	var edit track.MetadataEdit
	reader, err := r.MultipartReader()
	if err != nil {
		return edit, fmt.Errorf("upload the image using multipart/form-data")
	}
	var metadata []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return edit, fmt.Errorf("could not read the image upload: %w", err)
		}
		limit := int64(maxRequestBytes)
		if part.FormName() == "artwork" {
			limit = artwork.MaxUploadBytes
		}
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		part.Close()
		if err != nil {
			return edit, err
		}
		if int64(len(data)) > limit {
			return edit, errArtworkUploadTooLarge
		}
		switch part.FormName() {
		case "metadata":
			if metadata != nil {
				return edit, fmt.Errorf("provide metadata only once")
			}
			metadata = data
		case "artwork":
			if edit.ArtworkData != nil {
				return edit, fmt.Errorf("upload one image at a time")
			}
			edit.ArtworkData = data
		default:
			return edit, fmt.Errorf("unexpected upload field")
		}
	}
	image := edit.ArtworkData
	if len(image) == 0 {
		return edit, fmt.Errorf("choose a cover image to upload")
	}
	if metadata != nil {
		decoder := json.NewDecoder(bytes.NewReader(metadata))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&edit); err != nil {
			return edit, fmt.Errorf("invalid track metadata: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return edit, fmt.Errorf("provide one metadata object")
		}
	}
	edit.ArtworkData = image
	return edit, nil
}
