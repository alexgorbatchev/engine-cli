// Package api exposes Engine operations as bounded asynchronous HTTP jobs.
package api

import (
	"context"
	"fmt"
	"github.com/dj/engine-cli/internal/systemcheck"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const maxJobs = 128
const maxEvents = 1024
const maxRequestBytes = 1 << 20

type Config struct {
	LibraryDir     string
	NoBackup       bool
	AllowedOrigins []string
	CheckSystem    func(context.Context, []string) (*systemcheck.Report, error)
}

type Server struct {
	config     Config
	mux        *http.ServeMux
	operations map[string]operation
	mu         sync.Mutex
	jobs       map[string]*job
	order      []string
	gate       chan struct{}
	closed     bool
	workers    sync.WaitGroup
}

func New(config Config) (*Server, error) {
	if config.LibraryDir == "" {
		return nil, fmt.Errorf("library directory is required")
	}
	path, err := filepath.Abs(config.LibraryDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !info.IsDir() {
		return nil, fmt.Errorf("library path must be a directory")
	}
	config.LibraryDir = path
	config.AllowedOrigins = slices.Clone(config.AllowedOrigins)
	for _, origin := range config.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("allowed origin must be an exact http(s) origin")
		}
	}
	s := &Server{config: config, mux: http.NewServeMux(), operations: make(map[string]operation), jobs: make(map[string]*job), gate: make(chan struct{}, 1)}
	ops := append(s.libraryOperations(), s.playlistOperations()...)
	ops = append(ops, s.trackOperations()...)
	ops = append(ops, s.historyOperations()...)
	ops = append(ops, s.duplicateOperations()...)
	ops = append(ops, s.systemOperations()...)
	for _, op := range ops {
		s.operations[op.Name] = op
	}
	s.mux.HandleFunc("GET /v1/operations", s.catalog)
	s.mux.HandleFunc("GET /v1/system/check", s.systemCheck)
	s.mux.HandleFunc("GET /v1/tracks", s.batchDetails("track"))
	s.mux.HandleFunc("GET /v1/playlists", s.batchDetails("playlist"))
	s.mux.HandleFunc("POST /v1/operations/{operation}", s.submit)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.status)
	s.mux.HandleFunc("POST /v1/jobs/{id}/cancel", s.cancel)
	s.mux.HandleFunc("GET /v1/jobs/{id}/events", s.events)
	s.mux.HandleFunc("GET /v1/tracks/{id}", s.trackMetadata)
	s.mux.HandleFunc("GET /v1/tracks/{id}/audio", s.trackAudio)
	s.mux.HandleFunc("GET /v1/tracks/{id}/beatgrid", s.trackBeatGrid)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		writeError(w, http.StatusForbidden, "invalid_host", "use a loopback address")
		return
	}
	origin := r.Header.Get("Origin")
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin != "" {
		if origin != scheme+"://"+r.Host && !slices.Contains(s.config.AllowedOrigins, origin) {
			writeError(w, http.StatusForbidden, "origin_denied", "browser origin is not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Close rejects new jobs, requests cancellation and joins every owned worker.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	for _, j := range s.jobs {
		j.cancel()
	}
	s.mu.Unlock()
	s.workers.Wait()
}
