package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type smartlistJournalReference struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

func (p *smartlistPrepared) referenceEntry(lib string, entry *Entry) error {
	if p.references == nil {
		p.references = make(map[string]string)
	}
	for _, side := range []*snapshot{entry.Before, entry.After} {
		if side == nil {
			continue
		}
		for _, state := range side.Databases {
			rel, err := filepath.Rel(lib, state.Path)
			if err != nil {
				return err
			}
			if previous, found := p.references[rel]; found {
				if previous != state.Hash {
					return errors.New("conflicting Smartlist reference fingerprints")
				}
				continue
			}
			p.references[rel] = state.Hash
			p.journal.References = append(p.journal.References, smartlistJournalReference{Path: rel, Hash: state.Hash})
		}
	}
	return nil
}

func smartlistReferencePath(dir, relative string) (string, error) {
	if !smartlistRelativePath(relative) {
		return "", errors.New("invalid Smartlist image reference")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) < 4 || parts[0] != ".engine-cli" || (parts[1] != "history" && parts[1] != "smartlist-repairs") {
		return "", errors.New("Smartlist image reference is outside native history storage")
	}
	path := filepath.Dir(filepath.Dir(dir))
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", errors.New("Smartlist image reference is not confined native storage")
		}
	}
	return path, nil
}

func validateSmartlistReferences(ctx context.Context, dir string, references []smartlistJournalReference) error {
	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		if reference.Hash == "" || seen[reference.Path] {
			return errors.New("missing or duplicate Smartlist image reference")
		}
		seen[reference.Path] = true
		path, err := smartlistReferencePath(dir, reference.Path)
		if err != nil {
			return err
		}
		hash, err := fingerprint(ctx, path)
		if err != nil || hash != reference.Hash {
			return errors.Join(errors.New("Smartlist referenced image changed"), err)
		}
	}
	return nil
}

func syncSmartlistReferences(ctx context.Context, dir string, references []smartlistJournalReference) error {
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := smartlistReferencePath(dir, reference.Path)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return err
		}
		// The file Sync covers its data. Native atomic marker publication also
		// commits directory membership without a Windows directory flush.
		if strings.HasPrefix(reference.Path, filepath.Join(".engine-cli", "smartlist-repairs")+string(filepath.Separator)) {
			if err := writeAtomic(filepath.Join(filepath.Dir(path), ".smartlist-images-ready"), []byte("ready\n")); err != nil {
				return err
			}
		}
	}
	return nil
}

func createSmartlistDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Smartlist storage is not a native directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Smartlist storage parent is not native")
	}
	staged, err := os.MkdirTemp(parent, ".smartlist-directory-")
	if err != nil {
		return err
	}
	if err := publishEntry(staged, path); err != nil {
		// A rename may already have succeeded. Retain any created directory as
		// preparation evidence; no live publication has been authorized yet.
		return err
	}
	return nil
}
