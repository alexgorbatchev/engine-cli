package tags

import (
	"context"
	"database/sql"
	"slices"
)

func CheckEdit(ctx context.Context, library string, edit Edit, category bool) error {
	_, key, err := normalizedName(edit.Name)
	if err != nil {
		return err
	}
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	if err := checkRevision(edit.Revision, s); err != nil {
		return err
	}
	if category {
		_, err = checkCategoryEdit(s, edit, key)
	} else {
		_, _, err = checkValueEdit(s, edit, key)
	}
	return err
}

func ValidateAssignment(edit AssignmentEdit) error {
	if edit.Values == nil {
		return failure("tag_values_required", "provide the complete value list, or an empty list to clear custom tags")
	}
	values := slices.Clone(edit.Values)
	slices.Sort(values)
	if len(slices.Compact(values)) != len(edit.Values) {
		return failure("tag_value_duplicate", "a custom-tag value may be assigned only once")
	}
	return nil
}

func CheckAssignment(ctx context.Context, engine *sql.DB, library string, edit AssignmentEdit) error {
	if err := ValidateAssignment(edit); err != nil {
		return err
	}
	if _, _, _, err := trackOrigin(ctx, engine, edit.Track); err != nil {
		return err
	}
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	if err := checkRevision(edit.Revision, s); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, c := range s.Categories {
		for _, v := range c.Values {
			known[v.ID] = true
		}
	}
	for _, id := range edit.Values {
		if !known[id] {
			return failure("tag_not_found", "a selected custom-tag value no longer exists in this library")
		}
	}
	return nil
}

func CheckDelete(ctx context.Context, library string, options DeleteOptions, category bool) error {
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	result, err := deletion(ctx, library, s, options, category)
	if err != nil {
		return err
	}
	if !options.DryRun && (options.PlanToken == "" || options.PlanToken != result.PlanToken) {
		return failure("tag_plan_stale", "review the current custom-tag deletion before applying it")
	}
	return nil
}

func checkCategoryEdit(s State, edit Edit, key string) (string, error) {
	found := edit.ID == ""
	previous := ""
	for _, c := range s.Categories {
		_, existingKey, err := normalizedName(c.Name)
		if err != nil {
			return "", err
		}
		if existingKey == key && c.ID != edit.ID {
			return "", failure("tag_name_exists", "a category already uses that name")
		}
		if c.ID == edit.ID {
			found, previous = true, c.Name
		}
	}
	if !found {
		return "", failure("tag_not_found", "the custom-tag category no longer exists")
	}
	return previous, nil
}

func checkValueEdit(s State, edit Edit, key string) (string, string, error) {
	category, previous := "", ""
	if edit.ID == "" {
		category = edit.Category
	}
	for _, c := range s.Categories {
		for _, v := range c.Values {
			if v.ID == edit.ID {
				category, previous = c.ID, v.Name
			}
		}
	}
	found := false
	for _, c := range s.Categories {
		if c.ID != category {
			continue
		}
		found = true
		for _, v := range c.Values {
			_, existingKey, err := normalizedName(v.Name)
			if err != nil {
				return "", "", err
			}
			if existingKey == key && v.ID != edit.ID {
				return "", "", failure("tag_name_exists", "a value in this category already uses that name")
			}
		}
	}
	if !found {
		return "", "", failure("tag_not_found", "the custom-tag category or value no longer exists")
	}
	return category, previous, nil
}
