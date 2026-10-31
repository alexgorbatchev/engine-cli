package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const smartlistJournalName = "pending.json"

// This is the durable evidence boundary, not a live publisher. The protected
// repair owner must hold the collection exclusion and history lock throughout
// preparation, publication and recovery. No read operation calls recovery.
type smartlistJournal struct {
	Version    int                         `json:"version"`
	Library    string                      `json:"library_uuid"`
	Images     []smartlistJournalImage     `json:"images"`
	References []smartlistJournalReference `json:"references,omitempty"`
}

type smartlistJournalImage struct {
	Target       string `json:"target"`
	Database     bool   `json:"database"`
	Original     string `json:"original,omitempty"`
	OriginalHash string `json:"original_hash,omitempty"`
	Staged       string `json:"staged"`
	StagedHash   string `json:"staged_hash"`
}

// prepareSmartlistJournal publishes intent only after every staged/original
// image has been verified and synced. All images already belong to this private
// repair directory. Aliased originals may be shared; they are never overwritten.
func prepareSmartlistJournal(ctx context.Context, dir string, journal smartlistJournal) error {
	if err := validateSmartlistJournal(ctx, dir, journal); err != nil {
		return err
	}
	path := filepath.Join(dir, smartlistJournalName)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("Smartlist repair intent already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, image := range journal.Images {
		for _, name := range []string{image.Original, image.Staged} {
			if name == "" {
				continue
			}
			// Windows FlushFileBuffers requires a writable handle. These are
			// private evidence copies; opening read/write never changes bytes.
			file, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR, 0)
			if err != nil {
				return err
			}
			if err := errors.Join(file.Sync(), file.Close()); err != nil {
				return err
			}
		}
	}
	if err := syncSmartlistReferences(ctx, dir, journal.References); err != nil {
		return err
	}
	// Recheck after syncing, before granting any future publisher permission to
	// touch live files. Cancellation before intent never enrolls a pending repair.
	if err := validateSmartlistJournal(ctx, dir, journal); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func readSmartlistJournal(ctx context.Context, dir string) (*smartlistJournal, error) {
	path, err := smartlistJournalFile(dir, smartlistJournalName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var journal smartlistJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, err
	}
	if err := validateSmartlistJournal(ctx, dir, journal); err != nil {
		return nil, err
	}
	return &journal, nil
}

func validateSmartlistJournal(ctx context.Context, dir string, journal smartlistJournal) error {
	if journal.Version != 1 || journal.Library == "" || len(journal.Images) == 0 {
		return errors.New("invalid Smartlist repair intent")
	}
	if info, err := os.Lstat(dir); err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Smartlist repair storage is not a directory")
	}
	targets := make(map[string]bool, len(journal.Images))
	var originals, stages []os.FileInfo
	for _, image := range journal.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !smartlistRelativePath(image.Target) || targets[image.Target] {
			return errors.New("invalid or duplicate Smartlist repair target")
		}
		targets[image.Target] = true
		if (image.Original == "") != (image.OriginalHash == "") {
			return errors.New("incomplete Smartlist repair original")
		}
		if image.Database && image.Original == "" {
			return errors.New("Smartlist database repair requires an original")
		}
		if image.Staged == "" || image.StagedHash == "" || image.Staged == image.Original {
			return errors.New("missing or aliased Smartlist repair stage")
		}
		for _, proof := range []struct{ name, hash string }{{image.Original, image.OriginalHash}, {image.Staged, image.StagedHash}} {
			if proof.name == "" {
				continue
			}
			path, err := smartlistJournalFile(dir, proof.name)
			if err != nil {
				return err
			}
			digest, err := smartlistJournalHash(ctx, path, image.Database)
			if err != nil {
				return err
			}
			if digest != proof.hash {
				return fmt.Errorf("Smartlist repair evidence changed: %s", proof.name)
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if proof.name == image.Original {
				originals = append(originals, info)
			} else {
				stages = append(stages, info)
			}
		}
	}
	for _, original := range originals {
		for _, stage := range stages {
			if os.SameFile(original, stage) {
				return errors.New("Smartlist repair stage aliases an original image")
			}
		}
	}
	return validateSmartlistReferences(ctx, dir, journal.References)
}

func smartlistRelativePath(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.IsLocal(path) && filepath.Clean(path) == path
}

// Images are immediate children of the private repair directory. Publishing
// pending.json syncs that same directory, making the prior image entries durable
// without relying on unsynced nested directories.
func smartlistJournalFile(dir, name string) (string, error) {
	if !smartlistRelativePath(name) || filepath.Base(name) != name {
		return "", errors.New("Smartlist repair evidence escapes its directory")
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Smartlist repair evidence is not a confined regular file")
	}
	return path, nil
}

func smartlistJournalHash(ctx context.Context, path string, database bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if database {
		return fingerprint(ctx, path)
	}
	state, err := fileHash(path)
	return state.Hash, err
}
