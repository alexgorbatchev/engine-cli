package history

import (
	"os"
	"path/filepath"
)

// sameDatabases reports whether every database a collection entry captured
// is byte-for-byte unchanged after the operation.
func sameDatabases(before, after *snapshot) bool {
	if before == nil || after == nil || len(before.Databases) != len(after.Databases) {
		return false
	}
	for path, state := range before.Databases {
		if after.Databases[path].Hash != state.Hash {
			return false
		}
	}
	return true
}

// keepFilesOnly records a collection entry that changed no database as a files
// entry and drops its unchanged database snapshots, so its undo restores only
// the files the operation changed.
func keepFilesOnly(e *Entry, work string) error {
	e.Scope = ScopeFiles
	e.Before.Databases = map[string]databaseState{}
	e.After.Databases = map[string]databaseState{}
	for _, side := range []string{"before", "after"} {
		if err := os.RemoveAll(filepath.Join(work, side)); err != nil {
			return err
		}
	}
	return nil
}
