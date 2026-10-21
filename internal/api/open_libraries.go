package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
)

const maxOpenLibraries = 64

var errLibraryServerClosed = errors.New("server is closed")

type openedLibrary struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Default bool   `json:"default"`
	server  *Server
}

func libraryPath(path string) (string, error) {
	return libraryops.Path(context.Background(), path)
}

func (s *Server) openLibraries(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return
	}
	raw, err := decodeRequest(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	var params struct {
		Paths []string `json:"paths"`
	}
	if err == nil {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&params)
	}
	if err != nil || len(params.Paths) == 0 || len(params.Paths) > maxOpenLibraries {
		writeError(w, http.StatusBadRequest, "invalid_paths", "provide paths for 1 to 64 existing Engine libraries")
		return
	}
	opened, err := s.openLibraryPaths(params.Paths)
	if err != nil {
		if errors.Is(err, errLibraryServerClosed) {
			writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_library", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, opened)
}

func (s *Server) openLibraryPaths(requested []string) ([]openedLibrary, error) {
	paths := make([]string, 0, len(requested))
	seen := make(map[string]bool)
	for _, path := range requested {
		canonical, err := libraryPath(path)
		if err != nil {
			return nil, err
		}
		if !seen[canonical] {
			paths = append(paths, canonical)
			seen[canonical] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errLibraryServerClosed
	}
	count := len(s.libraries)
	for _, path := range paths {
		if _, ok := s.libraries[path]; !ok {
			count++
		}
	}
	if count > maxOpenLibraries {
		return nil, fmt.Errorf("at most 64 libraries can be open")
	}
	opened := make([]openedLibrary, 0, len(paths))
	pending := make(map[string]openedLibrary)
	published := false
	defer func() {
		if !published {
			for _, lib := range pending {
				if lib.server != s {
					lib.server.Close()
				}
			}
		}
	}()
	for _, path := range paths {
		lib, ok := s.libraries[path]
		if !ok {
			hash := sha256.Sum256([]byte(path))
			lib = openedLibrary{Path: path, URL: "/v1/libraries/" + hex.EncodeToString(hash[:])}
			if canonical, err := filepath.EvalSymlinks(s.config.LibraryDir); err == nil && canonical == path {
				lib.server = s
				lib.Default = true
			} else {
				config := s.config
				config.LibraryDir = path
				config.LibraryPaths = nil
				child, err := New(config)
				if err != nil {
					return nil, err
				}
				// Cross-library sync shares the same execution gate as every opened library.
				child.gate = s.gate
				lib.server = child
			}
			pending[path] = lib
		}
		opened = append(opened, lib)
	}
	for path := range pending {
		if err := s.state.Remember(context.Background(), path); err != nil {
			return nil, err
		}
	}
	for path, lib := range pending {
		s.libraries[path] = lib
	}
	published = true
	return opened, nil
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	libs := make([]openedLibrary, 0, len(s.libraries))
	for _, lib := range s.libraries {
		libs = append(libs, lib)
	}
	sort.Slice(libs, func(i, j int) bool { return libs[i].Path < libs[j].Path })
	writeJSON(w, http.StatusOK, libs)
}

func (s *Server) libraryRequest(w http.ResponseWriter, r *http.Request) {
	resource := r.PathValue("resource")
	if resource == "libraries" || strings.HasPrefix(resource, "libraries/") {
		writeError(w, http.StatusNotFound, "unknown_resource", "manage opened libraries at /v1/libraries")
		return
	}
	prefix := "/v1/libraries/" + r.PathValue("library")
	s.mu.Lock()
	var child *Server
	for _, lib := range s.libraries {
		if lib.URL == prefix {
			child = lib.server
			break
		}
	}
	s.mu.Unlock()
	if child == nil {
		writeError(w, http.StatusNotFound, "unknown_library", "library is not open")
		return
	}
	request := r.Clone(r.Context())
	request.URL.Path = "/v1/" + resource
	request.URL.RawPath = ""
	child.mux.ServeHTTP(libraryResponse{ResponseWriter: w, prefix: prefix}, request)
}

type libraryResponse struct {
	http.ResponseWriter
	prefix string
}

func (w libraryResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w libraryResponse) WriteHeader(status int) {
	if location := w.Header().Get("Location"); strings.HasPrefix(location, "/v1/") {
		w.Header().Set("Location", w.prefix+strings.TrimPrefix(location, "/v1"))
	}
	w.ResponseWriter.WriteHeader(status)
}
