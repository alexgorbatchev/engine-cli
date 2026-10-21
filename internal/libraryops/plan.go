package libraryops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func validate(opts Options) error {
	if opts.Path == "" {
		return fmt.Errorf("library path is required")
	}
	switch opts.Action {
	case "delete":
	case "move":
		if opts.Destination == "" {
			return fmt.Errorf("destination is required")
		}
	case "aggregate":
		if opts.Mode != "copy" && opts.Mode != "move" {
			return fmt.Errorf("mode must be copy or move")
		}
		if opts.SharedMode != "copy" && opts.SharedMode != "move" {
			return fmt.Errorf("shared_mode must be copy or move")
		}
	default:
		return fmt.Errorf("unknown library action")
	}
	return nil
}

func Preview(ctx context.Context, opts Options, known []Library) (*Plan, error) {
	if opts.Mode == "" {
		opts.Mode = "copy"
	}
	if opts.SharedMode == "" {
		opts.SharedMode = "copy"
	}
	if err := validate(opts); err != nil {
		return nil, err
	}
	path, err := Path(ctx, opts.Path)
	if err != nil {
		return nil, err
	}
	directory, err := os.Lstat(filepath.Join(path, "Database2"))
	if err != nil {
		return nil, err
	}
	if directory.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("managed database directory must not be a symbolic link")
	}
	dbInfo, err := os.Lstat(filepath.Join(path, "Database2", "m.db"))
	if err != nil {
		return nil, err
	}
	if dbInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("managed library database must not be a symbolic link")
	}
	tracks, hash, err := scan(ctx, path)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: path, Action: opts.Action, Applicable: true, Files: []File{}, AffectedLibraries: []Library{}, UnavailableLibraries: []Library{}, Warnings: []string{}, databases: map[string]string{}}
	p.databases[path] = hash
	if opts.Action == "move" {
		p.Destination, err = filepath.Abs(opts.Destination)
		if err != nil {
			return nil, err
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(p.Destination))
		if e != nil {
			return nil, fmt.Errorf("destination parent must exist: %w", e)
		}
		p.Destination = filepath.Join(parent, filepath.Base(p.Destination))
		if inside(path, p.Destination) || inside(p.Destination, path) {
			return nil, fmt.Errorf("library locations must not overlap")
		}
		if _, e = os.Lstat(p.Destination); !os.IsNotExist(e) {
			return nil, fmt.Errorf("destination must not exist")
		}
	}
	shared := map[string][]Library{}
	seen := map[string]bool{path: true}
	for _, lib := range known {
		lib.Path = mediaPath("", lib.Path)
		if seen[lib.Path] {
			continue
		}
		seen[lib.Path] = true
		if info, e := os.Stat(filepath.Join(lib.Path, "Database2", "m.db")); e == nil && os.SameFile(dbInfo, info) && opts.Action != "delete" {
			p.Applicable = false
			p.Warnings = append(p.Warnings, "shared_database")
		}
		if inside(path, lib.Path) || inside(lib.Path, path) {
			p.Applicable = false
			p.Warnings = append(p.Warnings, "overlapping_library")
		}
		other, hash, e := scan(ctx, lib.Path)
		if e != nil {
			p.UnavailableLibraries = append(p.UnavailableLibraries, lib)
			continue
		}
		p.databases[lib.Path] = hash
		references := map[string]bool{}
		for _, t := range other {
			if !references[t.path] {
				shared[t.path] = append(shared[t.path], lib)
				references[t.path] = true
			}
		}
	}
	byPath := map[string]*File{}
	for _, t := range tracks {
		if f := byPath[t.path]; f != nil {
			f.IDs = append(f.IDs, t.id)
			continue
		}
		f := &File{Path: t.path, IDs: []int64{t.id}, Libraries: shared[t.path], Action: "keep"}
		if f.Libraries == nil {
			f.Libraries = []Library{}
		}
		switch opts.Action {
		case "delete":
			if opts.DeleteMedia && !(opts.KeepShared && (len(f.Libraries) > 0 || len(p.UnavailableLibraries) > 0)) {
				f.Action = "delete"
			}
		case "aggregate":
			if !inside(path, t.path) {
				hash := sha256.Sum256([]byte(t.path))
				f.Destination = filepath.Join(path, "Media", hex.EncodeToString(hash[:]), filepath.Base(t.path))
				f.Action = opts.Mode
				if opts.Mode == "move" && opts.SharedMode == "copy" && (len(f.Libraries) > 0 || len(p.UnavailableLibraries) > 0) {
					f.Action = "copy"
				}
			}
		case "move":
			if inside(path, t.path) {
				f.Action = "move"
				rel, _ := filepath.Rel(path, t.path)
				f.Destination = filepath.Join(p.Destination, rel)
			}
		}
		f.Hash, err = digest(ctx, t.path)
		if os.IsNotExist(err) {
			f.Missing = true
			f.Action = "keep"
			if opts.Action == "aggregate" {
				p.Applicable = false
				p.Warnings = append(p.Warnings, "missing_media")
			}
		} else if err != nil {
			return nil, err
		}
		if f.Destination != "" && opts.Action == "aggregate" {
			for target := f.Destination; target != path; target = filepath.Dir(target) {
				info, e := os.Lstat(target)
				if e != nil && !os.IsNotExist(e) {
					return nil, e
				}
				if e == nil && info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("media destination must not contain symbolic links")
				}
			}
			hash, e := digest(ctx, f.Destination)
			if e == nil && hash != f.Hash {
				return nil, fmt.Errorf("destination contains different media")
			}
			if e != nil && !os.IsNotExist(e) {
				return nil, e
			}
		}
		byPath[t.path] = f
	}
	affected := map[string]Library{}
	for _, f := range byPath {
		p.Files = append(p.Files, *f)
		if (opts.Action == "delete" && opts.DeleteMedia) || (opts.Action == "aggregate" && !inside(path, f.Path)) || (opts.Action == "move" && inside(path, f.Path)) {
			for _, lib := range f.Libraries {
				affected[lib.Path] = lib
			}
		}
	}
	for _, lib := range affected {
		p.AffectedLibraries = append(p.AffectedLibraries, lib)
	}
	if len(p.UnavailableLibraries) > 0 {
		p.Warnings = append(p.Warnings, "unavailable_libraries")
		if (opts.Action == "delete" && opts.DeleteMedia && !opts.KeepShared) || (opts.Action == "aggregate" && opts.Mode == "move" && opts.SharedMode == "move") || opts.Action == "move" {
			p.Applicable = false
		}
	}
	if opts.Action == "delete" {
		if err = planDelete(ctx, p); err != nil {
			return nil, err
		}
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	sort.Slice(p.AffectedLibraries, func(i, j int) bool { return p.AffectedLibraries[i].Path < p.AffectedLibraries[j].Path })
	sort.Slice(p.UnavailableLibraries, func(i, j int) bool { return p.UnavailableLibraries[i].Path < p.UnavailableLibraries[j].Path })
	opts.Path = path
	opts.Destination = p.Destination
	opts.DryRun = false
	opts.PlanToken = ""
	opts.ConfirmPath = ""
	input, err := json.Marshal(struct {
		Options   Options
		Plan      *Plan
		Databases map[string]string
	}{opts, p, p.databases})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(input)
	p.PlanToken = hex.EncodeToString(sum[:])
	return p, nil
}

func Check(ctx context.Context, opts Options, known []Library) (*Plan, error) {
	p, err := Preview(ctx, opts, known)
	if err != nil {
		return nil, err
	}
	if !p.Applicable {
		return nil, &progress.Error{Code: "library_plan_blocked", Cause: fmt.Errorf("library changes require resolving the preview warnings")}
	}
	if opts.PlanToken == "" || opts.PlanToken != p.PlanToken {
		return nil, &progress.Error{Code: "stale_library_plan", Cause: fmt.Errorf("preview the current library and confirm its plan token")}
	}
	if opts.Action == "delete" && opts.ConfirmPath != p.Path {
		return nil, &progress.Error{Code: "library_confirmation_mismatch", Cause: fmt.Errorf("type the complete library path exactly")}
	}
	return p, nil
}
