package details

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

func ValidateRefs(refs []string) error {
	if len(refs) == 0 || len(refs) > MaxItems {
		return fmt.Errorf("provide between 1 and %d references", MaxItems)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "eng:") || len(strings.Split(ref, ":")) != 4 {
			return fmt.Errorf("invalid entity reference")
		}
	}
	return nil
}

func ReadRefs(ctx context.Context, d *sql.DB, kind string, refs []string) (*Report, error) {
	if kind != "track" && kind != "playlist" {
		return nil, fmt.Errorf("unknown entity kind %q", kind)
	}
	if err := ValidateRefs(refs); err != nil {
		return nil, err
	}
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return nil, err
	}
	entities, err := identity.List(ctx, d, lib)
	if err != nil {
		return nil, err
	}
	byRef := map[string]int64{}
	for _, e := range entities {
		if e.Kind == kind && e.Ref != "" {
			if _, ok := byRef[e.Ref]; ok {
				return nil, fmt.Errorf("ambiguous native origin identity")
			}
			byRef[e.Ref] = e.ID
		}
	}
	r := &Report{Items: make([]Item, 0, len(refs))}
	for _, ref := range refs {
		item := Item{Ref: ref, ID: byRef[ref]}
		if item.ID == 0 {
			item.Error = &ItemError{Code: "not_found", Message: "Reference is missing or belongs to a different library."}
		} else {
			item.Data, err = readItem(ctx, d, kind, item.ID)
			if err != nil {
				return nil, err
			}
		}
		r.Items = append(r.Items, item)
	}
	attachRefs(r, kind, entities)
	return r, nil
}

func attachRefs(r *Report, kind string, entities []identity.Entity) {
	refs := map[string]map[int64]string{"track": {}, "playlist": {}}
	for _, e := range entities {
		refs[e.Kind][e.ID] = e.Ref
	}
	for i := range r.Items {
		item := &r.Items[i]
		if item.Data == nil {
			continue
		}
		item.Ref = refs[kind][item.ID]
		item.Data["ref"] = item.Ref
		if tracks, ok := item.Data["tracks"].([]map[string]any); ok {
			for _, t := range tracks {
				t["ref"] = refs["track"][t["id"].(int64)]
			}
		}
	}
}

func enrichRefs(ctx context.Context, d *sql.DB, kind string, r *Report) error {
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		return err
	}
	registry, err := identity.Load(lib)
	if err != nil || registry == nil {
		return err
	}
	entities, err := identity.List(ctx, d, lib)
	if err != nil {
		return err
	}
	attachRefs(r, kind, entities)
	return nil
}
