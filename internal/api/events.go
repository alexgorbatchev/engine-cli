package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dj/engine-cli/internal/progress"
)

const heartbeatInterval = 15 * time.Second
const streamWriteTimeout = 10 * time.Second

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	after := uint64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		var err error
		after, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "Last-Event-ID must be a nonnegative integer")
			return
		}
	}
	s.mu.Lock()
	j, ok := s.jobs[r.PathValue("id")]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "unknown_job", "job not found or expired")
		return
	}
	if after > j.view.Progress.Sequence {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is ahead of the job")
		return
	}
	if after > 0 && after < j.events[0].Sequence-1 {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "expired_cursor", "events expired; retrieve job status and reconnect without a cursor")
		return
	}
	if terminal(j.view.State) && after == j.view.Progress.Sequence {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		pending := make([]progress.Event, 0)
		for _, event := range j.events {
			if event.Sequence > after {
				pending = append(pending, event)
			}
		}
		done := terminal(j.view.State)
		changed := j.changed
		s.mu.Unlock()
		for _, event := range pending {
			if after != 0 && event.Sequence != after+1 {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout)) // Unsupported by in-memory test writers.
			if _, err := fmt.Fprintf(w, "id: %d\nevent: progress\ndata: %s\n\n", event.Sequence, data); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			after = event.Sequence
		}
		if done {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-ticker.C:
			_ = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}
