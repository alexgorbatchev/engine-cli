package api

import (
	"context"
	"database/sql"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/duplicate"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type stagedIDParams struct {
	ID string `json:"id"`
}
type stagedIDsParams struct {
	IDs []string `json:"ids"`
}
type stagedImportParams struct {
	IDs        []string `json:"ids"`
	ToPlaylist string   `json:"to_playlist"`
	importAnalysis
}

func (p stagedImportParams) validate() error { return p.importAnalysis.validate() }

func (s *Server) stagingOperations() []operation {
	store := staging.New(s.config.LibraryDir)
	return []operation{
		define("staging.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) { return store.List(ctx) }),
		define("staging.inspect", false, []string{"id"}, func(ctx context.Context, p stagedIDParams) (any, error) { return store.Inspect(ctx, p.ID) }),
		define("staging.duplicates", false, []string{"id"}, func(ctx context.Context, p stagedIDParams) (any, error) {
			item, err := store.Inspect(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return s.database(ctx, func(d *sql.DB) (any, error) {
				return duplicate.Candidates(ctx, d, duplicate.Track{Title: item.Title, Artist: item.Artist}, "metadata")
			})
		}),
		defineEffect("staging.rm", "staging", []string{"ids"}, func(ctx context.Context, p stagedIDsParams) (any, error) {
			ids, err := store.Remove(ctx, p.IDs)
			return map[string]any{"removed": ids}, err
		}),
		define("staging.import", true, []string{"ids"}, func(ctx context.Context, p stagedImportParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				report, err := store.Import(p.importProgress(ctx), d, staging.ImportOptions{IDs: p.IDs, ToPlaylist: p.ToPlaylist})
				if err != nil || !p.Analyze {
					return report, err
				}
				ids := make([]int64, len(report.Imported))
				for i, imported := range report.Imported {
					ids[i] = imported.TrackID
				}
				analyzed, analysisErr := s.analyzeImports(ctx, d, ids, p.BPMRange)
				for i := range report.Imported {
					if outcome, ok := analyzed[report.Imported[i].TrackID]; ok {
						report.Imported[i].Analysis = &outcome
					}
				}
				// Imports stay committed when analysis stops early, so report them with the error.
				return report, analysisErr
			})
		}),
	}
}

func (s *Server) stagedList(w http.ResponseWriter, r *http.Request) {
	items, err := staging.New(s.config.LibraryDir).List(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "staging_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) stagedMedia(artwork bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		release, ok := s.beginCollectionRead(w, r.Context())
		if !ok {
			return
		}
		defer release()
		if r.Header.Get("Sec-Fetch-Dest") == "audio" && r.Header.Get("Sec-Fetch-Site") != "same-origin" && r.Header.Get("Origin") == "" {
			writeError(w, http.StatusForbidden, "origin_required", "Audio previews require an allowed browser origin.")
			return
		}
		f, err := staging.New(s.config.LibraryDir).Open(r.Context(), r.PathValue("id"), artwork)
		if err != nil {
			writeError(w, http.StatusNotFound, "staged_media_unavailable", "This staged media is unavailable.")
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeError(w, http.StatusNotFound, "staged_media_unavailable", "This staged media is unavailable.")
			return
		}
		typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
		if strings.EqualFold(filepath.Ext(info.Name()), ".m4a") {
			typ = "audio/mp4"
		}
		prefix := "audio/"
		if artwork {
			prefix = "image/"
		}
		if !strings.HasPrefix(typ, prefix) {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media", "This file type is not available for preview.")
			return
		}
		w.Header().Set("Content-Type", typ)
		if artwork {
			data, err := io.ReadAll(f)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "staged_media_unavailable", "This staged media is unavailable.")
				return
			}
			serveArtworkContent(w, r, data)
			return
		}
		w.Header().Set("Cache-Control", audioCacheControl)
		release()
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}
