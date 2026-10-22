package history

import (
	"os"
	"path/filepath"
	"strings"
)

// onlyWorkspaceChanges reports whether an entry captured nothing but changes to
// files inside the operation's declared workspaces.
func onlyWorkspaceChanges(e *Entry, workspaces []string) bool {
	if len(workspaces) == 0 || len(e.Moves) > 0 || len(e.Before.Databases) != len(e.After.Databases) {
		return false
	}
	for path, before := range e.Before.Databases {
		if after, ok := e.After.Databases[path]; !ok || after.Hash != before.Hash {
			return false
		}
	}
	for path, before := range e.Before.Files {
		if e.After.Files[path] != before && !insideAny(path, workspaces) {
			return false
		}
	}
	return true
}

func insideAny(path string, dirs []string) bool {
	for _, dir := range dirs {
		if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// discard removes an entry and the content only it preserved. The entry record
// goes first so an interruption can leave unreferenced storage, which the next
// operation collects, but never an entry pointing at missing snapshots.
func discard(dir string, e *Entry) error {
	if err := os.Remove(filepath.Join(dir, e.ID+".json")); err != nil {
		return err
	}
	others, err := entries(dir)
	if err != nil {
		return err
	}
	return collect(dir, others)
}
