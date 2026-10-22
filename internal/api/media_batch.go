package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

const maxMediaBatch = 200

type mediaSelection struct {
	identifier, stagingID, path    string
	identifiers, stagingIDs, paths []string
}

func (p mediaSelection) validate() error {
	families := 0
	for _, count := range []int{len(p.identifier), len(p.stagingID), len(p.path), len(p.identifiers), len(p.stagingIDs), len(p.paths)} {
		if count > 0 {
			families++
		}
	}
	if families != 1 {
		return fmt.Errorf("choose exactly one of identifier, staging_id, path, identifiers, staging_ids, or paths")
	}
	items := p.items()
	if len(items) > maxMediaBatch {
		return fmt.Errorf("select at most %d files", maxMediaBatch)
	}
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("media selection contains an empty item")
		}
	}
	return nil
}

func (p mediaSelection) items() []string {
	for _, values := range [][]string{p.identifiers, p.stagingIDs, p.paths} {
		if len(values) > 0 {
			return values
		}
	}
	for _, value := range []string{p.identifier, p.stagingID, p.path} {
		if value != "" {
			return []string{value}
		}
	}
	return nil
}

func (p mediaSelection) field() string {
	if p.stagingID != "" || len(p.stagingIDs) > 0 {
		return "staging_id"
	}
	if p.path != "" || len(p.paths) > 0 {
		return "path"
	}
	return "identifier"
}
func (p mediaSelection) rawPaths() []string {
	if p.field() == "path" {
		return p.items()
	}
	return nil
}
func (p mediaSelection) effect() string {
	switch p.field() {
	case "staging_id":
		return "staging"
	case "path":
		return "filesystem"
	default:
		return "collection"
	}
}
func (p mediaSelection) isBatch() bool {
	return len(p.identifiers) > 0 || len(p.stagingIDs) > 0 || len(p.paths) > 0
}

// Each entry retains its selector and any native result, including failed results.
// An expected per-file failure does not hide successful siblings or their undo.
func (s *Server) tagSelection(ctx context.Context, p mediaSelection, action string, opts acquire.TagOptions) (any, error) {
	if !p.isBatch() {
		return s.tagItem(ctx, p.field(), p.items()[0], action, opts)
	}
	results := make([]map[string]any, 0, len(p.items()))
	failed := 0
	for _, item := range p.items() {
		if err := ctx.Err(); err != nil {
			return map[string]any{"status": "error", "error": err.Error(), "results": results}, err
		}
		result, err := s.tagItem(ctx, p.field(), item, action, opts)
		entry := map[string]any{p.field(): item, "result": result}
		if err != nil {
			entry["error"] = err.Error()
			failed++
		}
		results = append(results, entry)
	}
	if failed > 0 {
		err := fmt.Errorf("%d of %d files failed; inspect per-file results", failed, len(results))
		return map[string]any{"status": "error", "error": err.Error(), "results": results}, err
	}
	return map[string]any{"status": "success", "results": results}, nil
}

type mediaEffectKey struct{}

func (s *Server) tagItem(ctx context.Context, field, item, action string, opts acquire.TagOptions) (any, error) {
	client := s.toolClient()
	if action == "inspect" {
		path := item
		var err error
		if field != "path" {
			path, err = s.mediaPath(ctx, mediaParams{Identifier: mapValue(field, "identifier", item), StagingID: mapValue(field, "staging_id", item)})
		}
		if err != nil {
			return nil, err
		}
		return client.Tags(ctx, action, path, "", opts)
	}
	if field == "path" {
		path, err := filepath.EvalSymlinks(item)
		if err != nil {
			return nil, err
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		identifier, err := s.collectionPath(ctx, path)
		if err != nil {
			return nil, err
		}
		if identifier != "" {
			if !opts.DryRun && ctx.Value(mediaEffectKey{}) != "collection" {
				return nil, fmt.Errorf("file now belongs to the collection; retry with a fresh safety check")
			}
			field, item = "identifier", identifier
		} else {
			id, err := s.stagedPath(ctx, path)
			if err != nil {
				return nil, err
			}
			if id != "" {
				field, item = "staging_id", id
			} else {
				return track.UpdateUnmanagedFileTags(ctx, path, track.FileTagOptions{Options: opts, ArtworkOnly: action == "artwork", Runner: s.config.ToolRunner})
			}
		}
	}
	if field == "staging_id" {
		return s.acquisitionService().TagStaged(ctx, item, action, opts)
	}
	return s.database(ctx, func(d *sql.DB) (any, error) {
		return track.UpdateFileTags(ctx, d, s.config.LibraryDir, item, track.FileTagOptions{Options: opts, ArtworkOnly: action == "artwork", Runner: s.config.ToolRunner})
	})
}

func mapValue(field, want, value string) string {
	if field == want {
		return value
	}
	return ""
}

// Path aliases (symlinks and hard links) cannot bypass collection protection.
func (s *Server) collectionPath(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	} // A missing input is an individual file error.
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(s.config.LibraryDir, "Database2", "m.db")); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	value, err := s.database(ctx, func(d *sql.DB) (any, error) {
		rows, err := d.QueryContext(ctx, "SELECT id,path FROM Track")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var stored sql.NullString
			if err := rows.Scan(&id, &stored); err != nil {
				return nil, err
			}
			candidate := stored.String
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(s.config.LibraryDir, candidate)
			}
			other, err := os.Stat(candidate)
			if err == nil && os.SameFile(info, other) {
				return strconv.FormatInt(id, 10), nil
			}
		}
		return "", rows.Err()
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (s *Server) stagedPath(ctx context.Context, path string) (string, error) {
	store := staging.New(s.config.LibraryDir)
	items, err := store.List(ctx)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.AudioPath == "" {
			continue
		}
		candidate, err := store.Resolve(ctx, item.ID)
		if err != nil {
			return "", err
		}
		other, err := os.Stat(candidate)
		if err == nil && os.SameFile(info, other) {
			return item.ID, nil
		}
	}
	return "", nil
}

func (s *Server) mediaPathsEffect(ctx context.Context, paths []string) (string, error) {
	for _, path := range paths {
		id, err := s.collectionPath(ctx, path)
		if err != nil {
			return "", err
		}
		if id != "" {
			return "collection", nil
		}
	}
	return "filesystem", nil
}
