package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"sort"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type Job struct {
	ID              string                         `json:"id"`
	Operation       string                         `json:"operation"`
	State           string                         `json:"state"`
	Progress        progress.Event                 `json:"progress"`
	Result          any                            `json:"result,omitempty"`
	Error           *progress.Failure              `json:"error,omitempty"`
	HistoryID       string                         `json:"history_id,omitempty"`
	SmartlistRepair *history.SmartlistRepairResult `json:"smartlist_repair,omitempty"`
}

type job struct {
	view         Job
	events       []progress.Event
	changed      chan struct{}
	cancel       context.CancelFunc
	terminal     *progress.Event
	reduceResult func(any, json.RawMessage) any
}

func terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	ops := make([]operation, 0, len(s.operations))
	for _, op := range s.operations {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	writeJSON(w, http.StatusOK, ops)
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	op, ok := s.operations[r.PathValue("operation")]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_operation", "operation not found")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return
	}
	raw, err := decodeRequest(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	run, err := op.bind(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameters", err.Error())
		return
	}
	j, err := s.start(op, run)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) start(op operation, run *boundOperation) (Job, error) {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Job{}, err
	}
	id := hex.EncodeToString(idBytes[:])
	ctx, cancel := context.WithCancel(context.Background())
	started := false
	defer func() {
		if !started {
			cancel()
		}
	}()
	j := &job{view: Job{ID: id, Operation: op.Name, State: "queued"}, changed: make(chan struct{}), cancel: cancel, reduceResult: op.reduceResult}
	ctx = progress.WithJobReporter(ctx, id, func(e progress.Event) { s.record(j, e) })
	// Initialize the event stream before publishing the job to request handlers.
	progress.Emit(ctx, progress.Event{Type: "queued", Phase: op.Name})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, fmt.Errorf("server is shutting down")
	}
	if len(s.jobs) >= maxJobs {
		evicted := false
		for i, id := range s.order {
			if terminal(s.jobs[id].view.State) {
				delete(s.jobs, id)
				s.order = append(s.order[:i], s.order[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			return Job{}, fmt.Errorf("job capacity reached; retry later")
		}
	}
	s.jobs[id] = j
	s.order = append(s.order, id)
	accepted := j.view
	s.workers.Add(1)
	started = true
	go s.work(ctx, j, op, run)
	return accepted, nil
}

func (j *job) append(event progress.Event) {
	j.view.Progress = event
	if len(j.events) == maxEvents {
		copy(j.events, j.events[1:])
		j.events = j.events[:maxEvents-1]
	}
	j.events = append(j.events, event)
	close(j.changed)
	j.changed = make(chan struct{})
}

func (s *Server) record(j *job, e progress.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Type == "complete" || e.Type == "error" || e.Type == "cancelled" {
		j.terminal = &e
		return
	}
	if e.Type == "result" && j.reduceResult != nil {
		j.view.Result = j.reduceResult(j.view.Result, e.Result)
	}
	if e.Type == "smartlist_repair" {
		var repair history.SmartlistRepairResult
		if err := json.Unmarshal(e.Result, &repair); err == nil && repair.Applied {
			j.view.SmartlistRepair = &repair
		}
	}
	j.append(e)
}

func (s *Server) work(ctx context.Context, j *job, op operation, run *boundOperation) {
	defer s.workers.Done()
	defer j.cancel()
	release, err := s.acquireOperation(ctx, op, run)
	var result any
	var historyID string
	if err == nil {
		defer release()
		s.mu.Lock()
		j.view.State = "running"
		s.mu.Unlock()
		result, historyID, err = s.execute(ctx, op, run)
	} else {
		// Even jobs cancelled while queued own a terminal progress event.
		queueErr := err
		err = progress.Run(ctx, op.Name, func(context.Context) error { return queueErr })
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result != nil {
		j.view.Result = result
	}
	j.view.HistoryID = historyID
	j.view.State = "succeeded"
	if err != nil {
		j.view.State = "failed"
		if ctx.Err() != nil {
			j.view.State = "cancelled"
		}
		j.view.Error = &progress.Failure{Code: j.terminal.ErrorCode, Message: j.terminal.Error}
	}
	j.append(*j.terminal)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	j, ok := s.jobs[r.PathValue("id")]
	var view Job
	if ok {
		view = j.view
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_job", "job not found or expired")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	j, ok := s.jobs[r.PathValue("id")]
	var view Job
	if ok {
		j.cancel()
		view = j.view
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_job", "job not found or expired")
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_response", "response encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n')) // A disconnected client cannot receive a second error response.
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": progress.Failure{Code: code, Message: message}})
}
