package history

import (
	"path/filepath"
	"slices"
)

func portableEntry(e Entry) Entry {
	if e.Before != nil {
		s := *e.Before
		e.Before = &s
	}
	if e.After != nil {
		s := *e.After
		e.After = &s
	}
	e.Moves = slices.Clone(e.Moves)
	relocateEntry(&e, ".")
	return e
}

// Rebase only paths belonging to the library; external audio remains external.
func relocateEntry(e *Entry, library string) {
	if e.Library == library {
		return
	}
	relocate := func(path string) string {
		path = filepath.FromSlash(path)
		rel, err := filepath.Rel(e.Library, path)
		if err != nil || !filepath.IsLocal(rel) {
			return path
		}
		result := filepath.Join(library, rel)
		if library == "." {
			return filepath.ToSlash(result)
		}
		return result
	}
	for _, s := range []*snapshot{e.Before, e.After} {
		if s == nil {
			continue
		}
		files := make(map[string]fileState, len(s.Files))
		for path, state := range s.Files {
			files[relocate(path)] = state
		}
		s.Files = files
		databases := make(map[string]databaseState, len(s.Databases))
		for path, state := range s.Databases {
			state.Path = relocate(state.Path)
			databases[relocate(path)] = state
		}
		s.Databases = databases
	}
	for i := range e.Moves {
		e.Moves[i].From = relocate(e.Moves[i].From)
		e.Moves[i].To = relocate(e.Moves[i].To)
	}
	e.Library = library
}
