package api

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/libraryops"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type manageLibraryParams libraryops.Options

func (p manageLibraryParams) operationLibrary() string            { return p.Path }
func (p manageLibraryParams) operationPreview() bool              { return p.DryRun }
func (p manageLibraryParams) operationReplacementLibrary() string { return p.Destination }

func (s *Server) knownLibraries(ctx context.Context) ([]libraryops.Library, error) {
	entries, err := s.state.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]libraryops.Library, 0, len(entries))
	for _, lib := range entries {
		if lib.Retired {
			continue
		}
		result = append(result, libraryops.Library{Path: lib.Path, Name: lib.Name})
	}
	return result, nil
}

func (s *Server) libraryLifecycleOperations() []operation {
	result := []operation{}
	for _, item := range []struct{ name, action string }{{"library.delete", "delete"}, {"library.move", "move"}, {"library.media.aggregate", "aggregate"}} {
		// Applying a plan rechecks it and pins its canonical library before snapshots; previews need neither.
		before := func(ctx context.Context, p manageLibraryParams, bound *boundOperation) error {
			if p.DryRun {
				return nil
			}
			if err := s.libraryStatePlacement(p.Path); err != nil {
				return err
			}
			opts := libraryops.Options(p)
			opts.Action = item.action
			known, err := s.knownLibraries(ctx)
			if err != nil {
				return err
			}
			plan, err := libraryops.Check(ctx, opts, known)
			if err != nil {
				return err
			}
			bound.library = filepath.Clean(plan.Path)
			return nil
		}
		result = append(result, defineChecked(item.name, "collection", []string{"path"}, before, func(ctx context.Context, p manageLibraryParams) (any, error) {
			if err := s.libraryStatePlacement(p.Path); err != nil {
				return nil, err
			}
			opts := libraryops.Options(p)
			opts.Action = item.action
			known, err := s.knownLibraries(ctx)
			if err != nil {
				return nil, err
			}
			plan, err := libraryops.Preview(ctx, opts, known)
			if err != nil {
				return nil, err
			}
			if opts.DryRun {
				return plan, nil
			}
			if err = libraryops.Apply(ctx, opts, known); err != nil {
				return plan, err
			}
			if item.action == "delete" || item.action == "move" {
				if err = s.state.Retire(ctx, plan.Path); err != nil {
					return plan, err
				}
				if item.action == "move" {
					var name *string
					for _, lib := range known {
						if lib.Path == plan.Path && lib.Name != "" {
							value := lib.Name
							name = &value
						}
					}
					hidden := false
					if err = s.state.Update(ctx, plan.Destination, name, &hidden); err != nil {
						return plan, err
					}
				}
			}
			return plan, nil
		}))
	}
	return result
}

func (s *Server) libraryStatePlacement(path string) error {
	if s.config.StatePath == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	state, err := filepath.EvalSymlinks(s.config.StatePath)
	if err != nil {
		return err
	}
	lexical, e := filepath.Rel(root, s.config.StatePath)
	if e == nil && filepath.IsLocal(lexical) {
		return &progress.Error{Code: "state_database_inside_library", Cause: fmt.Errorf("state database must be outside the managed library")}
	}
	rel, err := filepath.Rel(root, state)
	if err == nil && filepath.IsLocal(rel) {
		return &progress.Error{Code: "state_database_inside_library", Cause: fmt.Errorf("state database must be outside the managed library")}
	}
	return err
}
