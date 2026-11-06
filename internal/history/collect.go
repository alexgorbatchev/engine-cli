package history

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// collect removes history storage that no entry references: saved files left by
// an interrupted discard, partially written objects and entries, and the work
// directory of an operation that stopped before recording its entry. It requires
// the history lock, which excludes every writer, and the complete entry list.
func collect(dir string, all []Entry) error {
	if err := checkSmartlistRepair(dir); err != nil {
		return err
	}
	ids := make(map[string]bool, len(all))
	referenced := make(map[string]bool)
	for _, e := range all {
		ids[e.ID] = true
		for _, state := range e.Before.Files {
			referenced[state.Hash] = true
		}
	}
	var errs []error
	objects := filepath.Join(dir, "objects")
	saved, err := os.ReadDir(objects)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, object := range saved {
		if !object.IsDir() && !referenced[object.Name()] {
			errs = append(errs, removeFile(filepath.Join(objects, object.Name())))
		}
	}
	stored, err := os.ReadDir(dir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, item := range stored {
		name := item.Name()
		switch {
		case item.IsDir() && isEntryID(name) && !ids[name]:
			errs = append(errs, os.RemoveAll(filepath.Join(dir, name)))
		case !item.IsDir() && strings.HasPrefix(name, entryTempPrefix):
			errs = append(errs, removeFile(filepath.Join(dir, name)))
		}
	}
	return errors.Join(errs...)
}

func isEntryID(name string) bool {
	id, err := hex.DecodeString(name)
	return err == nil && len(id) == entryIDBytes
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
