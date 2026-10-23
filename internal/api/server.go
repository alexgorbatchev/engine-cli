// Package api exposes Engine operations as bounded asynchronous HTTP jobs.
package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/state"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
	"github.com/alexgorbatchev/godeps"
	"golang.org/x/sync/semaphore"
)

const maxJobs = 128
const maxEvents = 1024
const maxRequestBytes = 1 << 20

type Config struct {
	StatePath      string
	state          *state.Store
	LibraryDir     string
	LibraryPaths   []string
	NoBackup       bool
	AllowedOrigins []string
	CheckSystem    func(context.Context, []string) (*systemcheck.Report, error)
	ArtworkRunner  artwork.CommandRunner
	ToolRunner     enginesync.CommandRunner
	// ToolInstaller replaces release downloads for managed tools.
	ToolInstaller godeps.Installer
}

type Server struct {
	state      *state.Store
	ownsState  bool
	config     Config
	mux        *http.ServeMux
	operations map[string]operation
	mu         sync.Mutex
	jobs       map[string]*job
	order      []string
	gate       chan struct{}
	collection *semaphore.Weighted
	closed     bool
	workers    sync.WaitGroup
	libraries  map[string]openedLibrary
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
	if config.StatePath != "" {
		config.StatePath, err = filepath.Abs(config.StatePath)
		if err != nil {
			return nil, err
		}
	}
	config.AllowedOrigins = slices.Clone(config.AllowedOrigins)
	for _, origin := range config.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("allowed origin must be an exact http(s) origin")
		}
	}
	s := &Server{config: config, mux: http.NewServeMux(), operations: make(map[string]operation), jobs: make(map[string]*job), gate: make(chan struct{}, 1), collection: semaphore.NewWeighted(collectionCapacity)}
	s.state = config.state
	if s.state == nil {
		s.state, err = state.Open(config.StatePath)
		if err != nil {
			return nil, err
		}
		s.ownsState = true
		s.config.state = s.state
	}
	s.libraries = make(map[string]openedLibrary)
	ops := append(s.libraryOperations(), s.playlistOperations()...)
	ops = append(ops, s.libraryStateOperations()...)
	ops = append(ops, s.libraryLifecycleOperations()...)
	ops = append(ops, s.trackOperations()...)
	ops = append(ops, s.historyOperations()...)
	ops = append(ops, s.duplicateOperations()...)
	ops = append(ops, s.systemOperations()...)
	ops = append(ops, s.migrationOperations()...)
	ops = append(ops, s.stagingOperations()...)
	ops = append(ops, s.acquisitionOperations()...)
	ops = append(ops, s.mediaOperations()...)
	ops = append(ops, s.toolOperations()...)
	for _, op := range ops {
		s.operations[op.Name] = op
	}
	s.mux.HandleFunc("GET /v1/operations", s.catalog)
	s.mux.HandleFunc("POST /v1/libraries", s.collectionRead(s.openLibraries))
	s.mux.HandleFunc("GET /v1/libraries", s.collectionRead(s.listLibraries))
	s.mux.HandleFunc("GET /v1/library-preferences", s.collectionRead(s.libraryPreferences))
	s.mux.HandleFunc("PATCH /v1/library-preferences", s.collectionRead(s.updateLibraryPreferences))
	s.mux.HandleFunc("/v1/libraries/{library}/{resource...}", s.libraryRequest)
	s.mux.HandleFunc("GET /v1/staging", s.collectionRead(s.stagedList))
	s.mux.HandleFunc("GET /v1/staging/{id}/audio", s.stagedMedia(false))
	s.mux.HandleFunc("GET /v1/staging/{id}/artwork", s.stagedMedia(true))
	s.mux.HandleFunc("GET /v1/system/check", s.systemCheck)
	s.mux.HandleFunc("GET /v1/tracks", s.collectionRead(s.batchDetails("track")))
	s.mux.HandleFunc("GET /v1/playlists", s.collectionRead(s.batchDetails("playlist")))
	s.mux.HandleFunc("POST /v1/operations/{operation}", s.submit)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.status)
	s.mux.HandleFunc("POST /v1/jobs/{id}/cancel", s.cancel)
	s.mux.HandleFunc("GET /v1/jobs/{id}/events", s.events)
	s.mux.HandleFunc("GET /v1/tracks/{id}", s.collectionRead(s.trackMetadata))
	s.mux.HandleFunc("PUT /v1/tracks/{id}", s.editTrackUpload)
	s.mux.HandleFunc("GET /v1/tracks/{id}/audio", s.trackAudio)
	s.mux.HandleFunc("GET /v1/tracks/{id}/artwork", s.collectionRead(s.trackArtwork))
	s.mux.HandleFunc("GET /v1/tracks/{id}/beatgrid", s.collectionRead(s.trackBeatGrid))
	paths := slices.Clone(config.LibraryPaths)
	if path, err := libraryPath(config.LibraryDir); err == nil {
		paths = append([]string{path}, paths...)
	}
	if _, err := s.openLibraryPaths(paths); err != nil {
		s.Close()
		return nil, err
	}
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
	isLoopbackHost := host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback())
	if !isLoopbackHost {
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
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
	children := make([]*Server, 0, len(s.libraries))
	for _, lib := range s.libraries {
		if lib.server != s {
			children = append(children, lib.server)
		}
	}
	for _, j := range s.jobs {
		j.cancel()
	}
	s.mu.Unlock()
	for _, child := range children {
		child.Close()
	}
	s.workers.Wait()
	if s.ownsState {
		_ = s.state.Close() /* Shutdown has no error-return channel. */
	}
}
