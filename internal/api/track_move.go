package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type foldersParams struct {
	Path string `json:"path"`
}

// checkMove binds a reviewed move to its plan token before Desktop checks,
// snapshots and history; a move without a token keeps the unreviewed contract.
func (s *Server) checkMove(ctx context.Context, p moveTrackParams, _ *boundOperation) error {
	if p.DryRun || p.PlanToken == "" {
		return nil
	}
	_, err := s.database(ctx, func(d *sql.DB) (any, error) {
		return nil, track.CheckMove(ctx, d, s.config.LibraryDir, p.Identifier, p.Destination, p.PlanToken, s.sharedLookup)
	})
	return err
}

// sharedLookup finds the other known libraries that use a file (F063).
func (s *Server) sharedLookup(ctx context.Context, file string) ([]string, []string, error) {
	known, err := s.knownLibraries(ctx)
	if err != nil {
		return nil, nil, err
	}
	shared, unavailable, err := libraryops.SharedWith(ctx, file, s.config.LibraryDir, known)
	if err != nil {
		return nil, nil, err
	}
	return libraryPaths(shared), libraryPaths(unavailable), nil
}

func libraryPaths(libraries []libraryops.Library) []string {
	paths := make([]string, len(libraries))
	for i, lib := range libraries {
		paths[i] = lib.Path
	}
	return paths
}

// foldersOperation lists folders a track's audio may move into, confined to
// the library's folder and the computer's Music folder.
func (s *Server) foldersOperation() operation {
	return define("library.folders", false, nil, func(_ context.Context, p foldersParams) (any, error) {
		return track.ListFolders(track.MoveRoots(s.config.LibraryDir), p.Path)
	})
}
