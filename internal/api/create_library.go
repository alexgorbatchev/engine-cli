package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

type createLibraryParams struct {
	Path string `json:"path"`
}

func (p createLibraryParams) operationLibrary() string { return p.Path }

func (p createLibraryParams) validate() error {
	_, err := os.Lstat(filepath.Join(p.Path, "Database2"))
	if err == nil {
		return fmt.Errorf("library database directory already exists: %w", os.ErrExist)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

type CreatedLibrary struct {
	Path string `json:"path"`
	UUID string `json:"uuid"`
}

func createLibrary(ctx context.Context, p createLibraryParams) (*CreatedLibrary, error) {
	path, err := filepath.Abs(p.Path)
	if err != nil {
		return nil, err
	}
	id, err := db.CreateLibrary(ctx, path)
	if err != nil {
		return nil, err
	}
	return &CreatedLibrary{Path: path, UUID: id}, nil
}
