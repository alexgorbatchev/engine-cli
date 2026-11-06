package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The lock-owning publisher validates the entire target set before its first
// publication or restore. Recovery must not overwrite an external third state.
func validateSmartlistTargets(ctx context.Context, lib, dir string, journal smartlistJournal, recovering bool) error {
	if err := validateSmartlistJournal(ctx, dir, journal); err != nil {
		return err
	}
	for _, image := range journal.Images {
		path, err := smartlistTargetPath(lib, image)
		if err != nil {
			return err
		}
		if image.Original == "" {
			if err := validateSmartlistRemovedEvidence(ctx, path, image.StagedHash); err != nil {
				return err
			}
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && image.Original == "" {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("Smartlist repair target is not a regular file")
		}
		hash, err := smartlistJournalHash(ctx, path, image.Database)
		if err != nil {
			return err
		}
		if hash != image.OriginalHash && (!recovering || hash != image.StagedHash) {
			return smartlistAmbiguous("Smartlist repair target changed outside the pending repair")
		}
	}
	return nil
}

func smartlistRemovedEntry(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".repair-removed")
}

func validateSmartlistRemovedEvidence(ctx context.Context, path, expected string) error {
	removed := smartlistRemovedEntry(path)
	info, err := os.Lstat(removed)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Smartlist removed-entry evidence is not regular")
	}
	hash, err := smartlistJournalHash(ctx, removed, false)
	if err != nil || hash != expected {
		return errors.Join(errors.New("Smartlist removed-entry evidence changed"), err)
	}
	return nil
}

// Target names come from the domain owner, but a persisted journal is still
// untrusted input. It can address only the native database or history entries.
func smartlistTargetPath(lib string, image smartlistJournalImage) (string, error) {
	if !smartlistRelativePath(image.Target) {
		return "", errors.New("invalid Smartlist repair target")
	}
	if image.Database {
		if image.Target != filepath.Join("Database2", "m.db") {
			return "", errors.New("invalid Smartlist repair database target")
		}
	} else {
		if filepath.Dir(image.Target) != filepath.Join(".engine-cli", "history") {
			return "", errors.New("invalid Smartlist repair history target")
		}
		name := filepath.Base(image.Target)
		id := strings.TrimSuffix(name, ".json")
		if !isEntryID(id) || name != id+".json" {
			return "", errors.New("invalid Smartlist repair entry identity")
		}
	}
	path := lib
	parts := strings.Split(image.Target, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Smartlist repair target parent is not a native directory")
		}
	}
	return filepath.Join(lib, image.Target), nil
}

func restoreSmartlistTargets(ctx context.Context, lib, dir string, journal smartlistJournal) error {
	if err := validateSmartlistTargets(ctx, lib, dir, journal, true); err != nil {
		return err
	}
	for _, image := range journal.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeSmartlistTarget(ctx, lib, dir, image, true); err != nil {
			return fmt.Errorf("restoring Smartlist repair target: %w", err)
		}
	}
	return validateSmartlistTargets(ctx, lib, dir, journal, false)
}

func writeSmartlistTarget(ctx context.Context, lib, dir string, image smartlistJournalImage, original bool) error {
	path, err := smartlistTargetPath(lib, image)
	if err != nil {
		return err
	}
	name := image.Staged
	if original {
		name = image.Original
	}
	if name == "" {
		if err := validateSmartlistRemovedEvidence(ctx, path, image.StagedHash); err != nil {
			return err
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		// Same-directory publication durably removes the entry from the JSON
		// inventory on both Unix and Windows. Retain the candidate as evidence.
		return publishEntry(path, smartlistRemovedEntry(path))
	}
	source, err := smartlistJournalFile(dir, name)
	if err != nil {
		return err
	}
	if image.Database {
		return copyDatabase(ctx, source, path)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}
