// Package discovery locates current Engine DJ libraries without opening SQLite.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dj/engine-cli/internal/progress"
)

const libraryName = "Engine Library"

type Library struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Path     string `json:"path"`
	Database string `json:"database"`
}

type location struct {
	Path   string
	Name   string
	Source string
}

func driveLocation(path string) location {
	name := filepath.Base(filepath.Clean(path))
	if path == string(filepath.Separator) {
		return location{Path: path, Name: "Local disk", Source: "drive"}
	}
	return location{Path: path, Name: "Drive: " + name, Source: "drive"}
}

type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type Report struct {
	Libraries []Library `json:"libraries"`
	Warnings  []Warning `json:"warnings"`
}

// Discover checks standard locations unless explicit search roots are supplied.
// Roots are checked directly and at Engine Library and Music/Engine Library.
// It never traverses audio folders, backups, or directory trees recursively.
func Discover(ctx context.Context, roots []string) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := &Report{Libraries: []Library{}, Warnings: []Warning{}}
	var locations []location
	if len(roots) == 0 {
		var warnings []Warning
		locations, warnings = defaultLocations()
		r.Warnings = append(r.Warnings, warnings...)
	} else {
		for _, root := range roots {
			locations = append(locations, location{Path: root, Name: "Folder: " + filepath.Base(filepath.Clean(root)), Source: "folder"})
		}
	}
	seen := make(map[string]bool)
	var found []os.FileInfo
	for i, loc := range locations {
		root := loc.Path
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if root == "" {
			return nil, fmt.Errorf("search path must not be empty")
		}
		for _, candidate := range []string{root, filepath.Join(root, libraryName), filepath.Join(root, "Music", libraryName)} {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			path, info, err := inspect(candidate)
			if err != nil {
				if !os.IsNotExist(err) && !seen[candidate] {
					var pathErr *os.PathError
					if errors.As(err, &pathErr) {
						err = pathErr.Err
					}
					r.Warnings = append(r.Warnings, Warning{Path: candidate, Message: err.Error()})
				}
				seen[candidate] = true
				continue
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			duplicate := false
			for _, previous := range found {
				if os.SameFile(previous, info) {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			found = append(found, info)
			r.Libraries = append(r.Libraries, Library{Name: loc.Name, Source: loc.Source, Path: path, Database: filepath.Join(path, "Database2", "m.db")})
		}
		progress.Item(ctx, "library.discover.locations", i+1, len(locations))
	}
	sort.Slice(r.Libraries, func(i, j int) bool { return r.Libraries[i].Path < r.Libraries[j].Path })
	sort.Slice(r.Warnings, func(i, j int) bool { return r.Warnings[i].Path < r.Warnings[j].Path })
	return r, nil
}

func inspect(candidate string) (string, os.FileInfo, error) {
	path, err := filepath.Abs(candidate)
	if err != nil {
		return "", nil, err
	}
	database := filepath.Join(path, "Database2", "m.db")
	info, err := os.Stat(database)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("database is not a regular file")
	}
	f, err := os.Open(database)
	if err != nil {
		return "", nil, err
	}
	var header [16]byte
	_, readErr := io.ReadFull(f, header[:])
	closeErr := f.Close()
	if readErr != nil || string(header[:]) != "SQLite format 3\x00" {
		return "", nil, fmt.Errorf("database does not have a SQLite header")
	}
	if closeErr != nil {
		return "", nil, closeErr
	}
	path, err = filepath.EvalSymlinks(path)
	return path, info, err
}

func homeLocations() ([]location, []Warning) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, []Warning{{Message: err.Error()}}
	}
	return []location{{Path: filepath.Join(home, "Music"), Name: "Local library", Source: "local"}}, nil
}
