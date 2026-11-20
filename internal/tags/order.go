package tags

import (
	"context"
	"database/sql"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

// MoveOptions places a category among the categories, or a value among its category's
// values, before the sibling Before names; an empty Before moves it to the end.
type MoveOptions struct {
	ID       string `json:"id"`
	Before   string `json:"before,omitempty"`
	Revision string `json:"revision"`
}

// ModeOptions makes a category multiple-choice or single-choice.
type ModeOptions struct {
	ID       string `json:"id"`
	Mode     string `json:"mode"`
	Revision string `json:"revision"`
}

// siblings returns the IDs a move reorders, in saved order, and the table that holds them.
func siblings(s State, id string, category bool) ([]string, string, bool) {
	if category {
		ids := make([]string, len(s.Categories))
		for i, c := range s.Categories {
			ids[i] = c.ID
		}
		return ids, "Category", slices.Contains(ids, id)
	}
	for _, c := range s.Categories {
		ids := make([]string, len(c.Values))
		for i, v := range c.Values {
			ids[i] = v.ID
		}
		if slices.Contains(ids, id) {
			return ids, "Value", true
		}
	}
	return nil, "Value", false
}

// reordered is the order after moving id before the anchor, or to the end without one.
func reordered(ids []string, options MoveOptions) ([]string, error) {
	if options.Before == options.ID {
		return nil, failure("tag_move_invalid", "a value cannot move before itself")
	}
	next := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == options.ID })
	at := len(next)
	if options.Before != "" {
		at = slices.Index(next, options.Before)
		if at < 0 {
			return nil, failure("tag_move_invalid", "move a category among categories and a value among its category's values")
		}
	}
	return slices.Insert(next, at, options.ID), nil
}

// CheckMove validates a move before the collection pipeline starts it.
func CheckMove(ctx context.Context, library string, options MoveOptions, category bool) error {
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	if err := checkRevision(options.Revision, s); err != nil {
		return err
	}
	ids, _, found := siblings(s, options.ID, category)
	if !found {
		return failure("tag_not_found", "the custom-tag category or value no longer exists")
	}
	_, err = reordered(ids, options)
	return err
}

// Move saves a new order; chips, filters and columns follow it.
func Move(ctx context.Context, library string, options MoveOptions, category bool) (*Mutation, error) {
	return mutate(ctx, library, options.Revision, func(tx *sql.Tx, s State) (string, bool, error) {
		ids, table, found := siblings(s, options.ID, category)
		if !found {
			return "", false, failure("tag_not_found", "the custom-tag category or value no longer exists")
		}
		next, err := reordered(ids, options)
		if err != nil {
			return "", false, err
		}
		if slices.Equal(ids, next) {
			return options.ID, false, nil
		}
		for position, id := range next {
			if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET position=? WHERE id=?", position, id); err != nil {
				return "", false, err
			}
		}
		return options.ID, true, nil
	})
}

// modeConflicts counts tracks that hold two or more of a category's values.
func modeConflicts(s State, c Category) int {
	held := map[companion.Origin]int{}
	for _, a := range s.Assignments {
		if slices.ContainsFunc(c.Values, func(v Value) bool { return v.ID == a.ValueID }) {
			held[companion.Origin{UUID: a.OriginUUID, ID: a.OriginTrackID}]++
		}
	}
	conflicts := 0
	for _, n := range held {
		if n > 1 {
			conflicts++
		}
	}
	return conflicts
}

func modeCategory(s State, options ModeOptions) (Category, error) {
	if options.Mode != ModeMultiple && options.Mode != ModeSingle {
		return Category{}, failure("tag_mode_invalid", "choose multiple or single")
	}
	i := slices.IndexFunc(s.Categories, func(c Category) bool { return c.ID == options.ID })
	if i < 0 {
		return Category{}, failure("tag_not_found", "the custom-tag category no longer exists")
	}
	c := s.Categories[i]
	// A category becomes single-choice only while no track holds two of its values.
	if options.Mode == ModeSingle && c.Mode != ModeSingle && modeConflicts(s, c) > 0 {
		return Category{}, failure("tag_mode_conflict", "some tracks hold more than one of this category's values; leave one on each first")
	}
	return c, nil
}

// CheckMode validates a mode change before the collection pipeline starts it.
func CheckMode(ctx context.Context, library string, options ModeOptions) error {
	s, err := Read(ctx, library)
	if err != nil {
		return err
	}
	if err := checkRevision(options.Revision, s); err != nil {
		return err
	}
	_, err = modeCategory(s, options)
	return err
}

// SetMode makes a category multiple-choice or single-choice.
func SetMode(ctx context.Context, library string, options ModeOptions) (*Mutation, error) {
	return mutate(ctx, library, options.Revision, func(tx *sql.Tx, s State) (string, bool, error) {
		c, err := modeCategory(s, options)
		if err != nil {
			return "", false, err
		}
		if c.Mode == options.Mode {
			return c.ID, false, nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE Category SET mode=? WHERE id=?", options.Mode, c.ID)
		return c.ID, err == nil, err
	})
}
