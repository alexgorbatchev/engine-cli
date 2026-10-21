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
	"github.com/alexgorbatchev/engine-cli/internal/state"
)

const maxOpenLibraries = 64

var errLibraryServerClosed = errors.New("server is closed")

type openedLibrary struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Key     string `json:"key"`
	Default bool   `json:"default"`
	server  *Server
}

// Listings carry the instance identity sync uses, so clients recognize a
// library across moves, server restarts, and registration.
type libraryListing struct {
	openedLibrary
	libraryops.Identity
}

type KnownLibrary struct {
	state.Library
	libraryops.Identity
}

// Identities are judged across every known library, so the opened and known
// listings agree about which libraries share an identity.
func (s *Server) describeLibraries(ctx context.Context, opened []openedLibrary) ([]state.Library, map[string]libraryops.Identity, error) {
	known, err := s.state.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(known)+len(opened))
	seen := make(map[string]bool)
	for _, lib := range known {
		paths = append(paths, lib.Path)
		seen[lib.Path] = true
	}
	for _, lib := range opened {
		if !seen[lib.Path] {
			paths = append(paths, lib.Path)
		}
	}
	identities, err := libraryops.DescribeAll(ctx, paths)
	if err != nil {
		return nil, nil, err
	}
	byPath := make(map[string]libraryops.Identity, len(paths))
	for i, path := range paths {
		byPath[path] = identities[i]
	}
	return known, byPath, nil
}

func (s *Server) listedLibraries(ctx context.Context, opened []openedLibrary) ([]libraryListing, error) {
	_, identities, err := s.describeLibraries(ctx, opened)
	if err != nil {
		return nil, err
	}
	listings := make([]libraryListing, len(opened))
	for i, lib := range opened {
		listings[i] = libraryListing{openedLibrary: lib, Identity: identities[lib.Path]}
	}
	return listings, nil
}

func (s *Server) libraryPreferenceListings(ctx context.Context) ([]KnownLibrary, error) {
	known, identities, err := s.describeLibraries(ctx, nil)
	if err != nil {
		return nil, err
	}
	listings := make([]KnownLibrary, len(known))
	for i, lib := range known {
		listings[i] = KnownLibrary{Library: lib, Identity: identities[lib.Path]}
	}
	return listings, nil
}

func (s *Server) writeLibraries(w http.ResponseWriter, r *http.Request, opened []openedLibrary) {
	listings, err := s.listedLibraries(r.Context(), opened)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_unavailable", "Library identities could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, listings)
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
	s.writeLibraries(w, r, opened)
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
			key := hex.EncodeToString(hash[:])
			lib = openedLibrary{Path: path, URL: "/v1/libraries/" + key, Key: key}
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
	libs := make([]openedLibrary, 0, len(s.libraries))
	for _, lib := range s.libraries {
		libs = append(libs, lib)
	}
	s.mu.Unlock()
	sort.Slice(libs, func(i, j int) bool { return libs[i].Path < libs[j].Path })
	// Identities are read outside the lock; reading databases must not block opening others.
	s.writeLibraries(w, r, libs)
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
