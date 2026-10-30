package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/pcm"
)

func TestPCMRequestsAreScopedAndValidated(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct {
		method, path, body, code string
		status                   int
	}{
		{"POST", "/v1/tracks/1/pcm", `{}`, "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":96000}`, "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":48000,"path":"/secret"}`, "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":48000} {}`, "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/0/pcm", `{"sample_rate":48000}`, "invalid_track", 400},
		{"POST", "/v1/tracks/99/pcm", `{"sample_rate":48000}`, "track_not_found", 404},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":48000}`, "audio_unavailable", 404},
		{"POST", "/v1/staging/missing/pcm", `{"sample_rate":48000}`, "staged_media_unavailable", 404},
		{"GET", "/v1/pcm/missing", "", "pcm_generation_unavailable", 404},
		{"DELETE", "/v1/pcm/missing", "", "pcm_generation_unavailable", 404},
		{"GET", "/v1/pcm/missing/windows?start_frame=0&frame_count=1", "", "pcm_generation_unavailable", 404},
		{"GET", "/v1/pcm/missing/windows?start_frame=-1&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=0&frame_count=96001", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=0&frame_count=1&path=/secret", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=0&start_frame=1&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=0", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=9223372036854775808&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=9223372036854775807&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=9223372036854775806&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=0&frame_count=999999999999999999999999", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=NaN&frame_count=1", "", "pcm_invalid_request", 400},
		{"GET", "/v1/pcm/missing/windows?start_frame=1.5&frame_count=1", "", "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":48000.5}`, "pcm_invalid_request", 400},
		{"POST", "/v1/tracks/1/pcm", `{"sample_rate":null}`, "pcm_invalid_request", 400},
	} {
		t.Run(tt.method+tt.path+tt.body, func(t *testing.T) {
			w := requestAPI(t, s, tt.method, tt.path, tt.body)
			if w.Code != tt.status || !strings.Contains(w.Body.String(), `"code":"`+tt.code+`"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/tracks/1/pcm", strings.NewReader(`{"sample_rate":48000}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != 415 {
		t.Fatalf("content type=%d", w.Code)
	}
}

func TestPCMErrorsNeverExposeDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		err    error
		code   string
		status int
	}{
		{pcm.ErrInvalid, "pcm_invalid_request", 400}, {pcm.ErrMissing, "pcm_generation_unavailable", 404},
		{pcm.ErrChanged, "pcm_source_changed", 409}, {pcm.ErrBusy, "pcm_busy", 503},
		{pcm.ErrClosed, "pcm_unavailable", 503}, {pcm.ErrUnavailable, "pcm_decoder_unavailable", 503},
		{pcm.ErrTimeline, "pcm_timeline_unsupported", 422}, {pcm.ErrExtentUnknown, "pcm_extent_unknown", 416},
		{context.Canceled, "pcm_cancelled", 408}, {errors.New("secret raw decoder diagnostic"), "pcm_decode_failed", 422},
	} {
		t.Run(tt.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			writePCMError(w, tt.err)
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) || strings.Contains(w.Body.String(), "secret raw") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestPCMRejectsDirectoryBeforeReleasingReadBarrier(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.Mkdir(filepath.Join(lib, "song.mp3"), 0700); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodPost, "/v1/tracks/1/pcm", `{"sample_rate":48000}`)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "audio_unavailable") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if !s.collection.TryAcquire(collectionCapacity) {
		t.Fatal("failed source open leaked collection read barrier")
	}
	s.collection.Release(collectionCapacity)
}
