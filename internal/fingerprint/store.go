package fingerprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type Index map[string]Audio

type storedIndex struct {
	Version int   `json:"version"`
	Items   Index `json:"items"`
}

func storePath(lib string) string {
	return filepath.Join(lib, ".engine-cli", "fingerprints", "index.json")
}

func Load(lib string) (Index, error) {
	b, err := os.ReadFile(storePath(lib))
	if os.IsNotExist(err) {
		return Index{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s storedIndex
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("reading fingerprint index: %w", err)
	}
	if s.Version != 1 || s.Items == nil {
		return nil, fmt.Errorf("unsupported or damaged fingerprint index")
	}
	return s.Items, nil
}

// Save participates in the enclosing library operation's affected-file history.
func Save(ctx context.Context, lib string, index Index) error {
	return history.WriteJSON(ctx, storePath(lib), storedIndex{Version: 1, Items: index})
}
