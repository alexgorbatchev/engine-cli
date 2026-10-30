package libraryops

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func planDelete(ctx context.Context, p *Plan) error {
	companions, err := companion.Databases(p.Path)
	if err != nil {
		return err
	}
	for _, path := range companions {
		hash, err := history.DatabaseFingerprint(ctx, path)
		if err != nil {
			return err
		}
		p.Files = append(p.Files, File{Path: path, Action: "database", Hash: hash, Libraries: []Library{}})
	}
	for _, dir := range []string{p.Path, filepath.Join(p.Path, "Database2")} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".db" {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("database symlinks must be resolved before deleting a library")
			}
			path := filepath.Join(dir, entry.Name())
			hash, err := history.DatabaseFingerprint(ctx, path)
			if err != nil {
				return err
			}
			p.Files = append(p.Files, File{Path: path, Action: "database", Hash: hash, Libraries: []Library{}})
		}
	}
	art := filepath.Join(p.Path, "Artwork")
	err = filepath.WalkDir(art, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("artwork symlinks must be resolved before deleting a library")
		}
		hash, err := digest(ctx, path)
		if err != nil {
			return err
		}
		p.Files = append(p.Files, File{Path: path, Action: "artwork", Hash: hash, Libraries: []Library{}})
		return nil
	})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if seen[f.Path] {
			return fmt.Errorf("media overlaps library databases or artwork")
		}
		seen[f.Path] = true
	}
	return nil
}

func deleteFiles(ctx context.Context, p *Plan) error {
	for _, file := range p.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.Action == "keep" {
			continue
		}
		if file.Action != "database" {
			if err := history.Preserve(ctx, file.Path); err != nil {
				return err
			}
		}
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if file.Action == "database" {
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if err := os.Remove(file.Path + suffix); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
	// Only empty managed directories are removed. Media, unrelated files and the
	// recovery journal are deliberately retained at the original library path.
	for _, dir := range []string{"Database2", "Artwork"} {
		_ = os.Remove(filepath.Join(p.Path, dir)) /* Nonempty directories are retained. */
	}
	return nil
}
