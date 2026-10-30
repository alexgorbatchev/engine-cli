package api

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

type tagCategoryCreateParams struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}
type tagCategoryUpdateParams struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}
type tagValueCreateParams struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}
type tagValueUpdateParams tagCategoryUpdateParams

func (p tagCategoryCreateParams) edit() tags.Edit {
	return tags.Edit{Name: p.Name, Revision: p.Revision}
}
func (p tagCategoryUpdateParams) edit() tags.Edit {
	return tags.Edit{ID: p.ID, Name: p.Name, Revision: p.Revision}
}
func (p tagValueCreateParams) edit() tags.Edit {
	return tags.Edit{Category: p.Category, Name: p.Name, Revision: p.Revision}
}
func (p tagValueUpdateParams) edit() tags.Edit {
	return tags.Edit{ID: p.ID, Name: p.Name, Revision: p.Revision}
}

type tagDeleteParams tags.DeleteOptions

func (p tagDeleteParams) operationPreview() bool { return p.DryRun }
func (p tagDeleteParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

type trackCustomTagsParams struct {
	Track string `json:"track"`
}
type assignCustomTagsParams tags.AssignmentEdit

type tagReconcileParams tags.ReconcileOptions

func (p tagReconcileParams) operationPreview() bool { return p.DryRun }
func (p tagReconcileParams) operationEffect() string {
	if p.DryRun {
		return "read"
	}
	return "collection"
}

func (p assignCustomTagsParams) validate() error {
	return tags.ValidateAssignment(tags.AssignmentEdit(p))
}

func tagEditOperation[P interface{ edit() tags.Edit }](s *Server, name string, required []string, category bool) operation {
	before := func(ctx context.Context, p P, _ *boundOperation) error {
		return tags.CheckEdit(ctx, s.config.LibraryDir, p.edit(), category)
	}
	return defineChecked(name, "collection", required, before, func(ctx context.Context, p P) (any, error) {
		if category {
			return tags.EditCategory(ctx, s.config.LibraryDir, p.edit())
		}
		return tags.EditValue(ctx, s.config.LibraryDir, p.edit())
	})
}

func (s *Server) tagDeleteOperation(category bool) operation {
	name := "tag.value.delete"
	if category {
		name = "tag.category.delete"
	}
	before := func(ctx context.Context, p tagDeleteParams, _ *boundOperation) error {
		return tags.CheckDelete(ctx, s.config.LibraryDir, tags.DeleteOptions(p), category)
	}
	return defineChecked(name, "collection", []string{"id", "revision"}, before, func(ctx context.Context, p tagDeleteParams) (any, error) {
		return tags.Delete(ctx, s.config.LibraryDir, tags.DeleteOptions(p), category)
	})
}

func (s *Server) tagOperations() []operation {
	return []operation{
		define("tag.category.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return tags.List(ctx, s.config.LibraryDir)
		}),
		defineChecked("tag.reconcile", "collection", []string{"revision"}, func(ctx context.Context, p tagReconcileParams, _ *boundOperation) error {
			return tags.CheckReconciliation(ctx, s.config.LibraryDir, tags.ReconcileOptions(p))
		}, func(ctx context.Context, p tagReconcileParams) (any, error) {
			return tags.Reconcile(ctx, s.config.LibraryDir, tags.ReconcileOptions(p))
		}),
		tagEditOperation[tagCategoryCreateParams](s, "tag.category.create", []string{"name", "revision"}, true),
		tagEditOperation[tagCategoryUpdateParams](s, "tag.category.update", []string{"id", "name", "revision"}, true),
		s.tagDeleteOperation(true),
		tagEditOperation[tagValueCreateParams](s, "tag.value.create", []string{"category", "name", "revision"}, false),
		tagEditOperation[tagValueUpdateParams](s, "tag.value.update", []string{"id", "name", "revision"}, false),
		s.tagDeleteOperation(false),
		define("track.custom-tags.inspect", false, []string{"track"}, func(ctx context.Context, p trackCustomTagsParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return tags.InspectTrack(ctx, d, s.config.LibraryDir, p.Track) })
		}),
		defineChecked("track.custom-tags.update", "collection", []string{"track", "revision", "values"}, func(ctx context.Context, p assignCustomTagsParams, _ *boundOperation) error {
			_, err := s.database(ctx, func(d *sql.DB) (any, error) {
				return nil, tags.CheckAssignment(ctx, d, s.config.LibraryDir, tags.AssignmentEdit(p))
			})
			return err
		}, func(ctx context.Context, p assignCustomTagsParams) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) { return tags.Assign(ctx, d, s.config.LibraryDir, tags.AssignmentEdit(p)) })
		}),
	}
}
