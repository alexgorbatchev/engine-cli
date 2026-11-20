package track

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// FolderRoot is a folder a move may target, with the name people know it by.
type FolderRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Folder is one child folder of a listing.
type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FolderListing lists one folder's child folders, with the roots a picker
// may move between and the parent when it is still inside a root.
type FolderListing struct {
	Roots   []FolderRoot `json:"roots"`
	Path    string       `json:"path"`
	Parent  string       `json:"parent,omitempty"`
	Folders []Folder     `json:"folders"`
}

// MoveRoots are the folders a track's audio may move within: the library's
// own folder and the computer's Music folder, when they exist.
func MoveRoots(lib string) []FolderRoot {
	roots := []FolderRoot{}
	for _, root := range []FolderRoot{{Label: "Library", Path: lib}, {Label: "Music", Path: musicFolder()}} {
		if root.Path == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(root.Path); err == nil {
			if info, err := os.Stat(real); err == nil && info.IsDir() && !containsRoot(roots, real) {
				roots = append(roots, FolderRoot{Label: root.Label, Path: real})
			}
		}
	}
	return roots
}

func musicFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Music")
}

func containsRoot(roots []FolderRoot, path string) bool {
	for _, root := range roots {
		if root.Path == path {
			return true
		}
	}
	return false
}

// ListFolders lists the child folders of path, the first root when path is
// empty. Paths outside every root are refused with folder_outside_roots, so a
// picker never browses the rest of the computer; hidden folders are skipped.
func ListFolders(roots []FolderRoot, path string) (*FolderListing, error) {
	if len(roots) == 0 {
		return nil, &progress.Error{Code: "folder_outside_roots", Cause: errors.New("no folder is available to move into")}
	}
	if path == "" {
		path = roots[0].Path
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	root, ok := rootOf(roots, real)
	if !ok {
		return nil, &progress.Error{Code: "folder_outside_roots", Cause: errors.New("the folder is outside the library and Music folders")}
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, err
	}
	listing := &FolderListing{Roots: roots, Path: real, Folders: []Folder{}}
	if real != root.Path {
		listing.Parent = filepath.Dir(real)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(real, entry.Name())
		if info, err := os.Stat(child); err == nil && info.IsDir() {
			listing.Folders = append(listing.Folders, Folder{Name: entry.Name(), Path: child})
		}
	}
	sort.Slice(listing.Folders, func(i, j int) bool {
		return strings.ToLower(listing.Folders[i].Name) < strings.ToLower(listing.Folders[j].Name)
	})
	return listing, nil
}

func rootOf(roots []FolderRoot, path string) (FolderRoot, bool) {
	for _, root := range roots {
		rel, err := filepath.Rel(root.Path, path)
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return root, true
		}
	}
	return FolderRoot{}, false
}
