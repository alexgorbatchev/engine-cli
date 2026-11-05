package tags

import (
	"bytes"
	"context"
	"testing"
)

func TestTagPreflightValidatesWithoutWriting(t *testing.T) {
	lib, engine, category, value := taggedTrack(t)
	revision := currentRevision(t, lib)
	preview, err := Delete(t.Context(), lib, DeleteOptions{ID: value, Revision: revision, DryRun: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	before := tagBytes(t, lib)
	for _, test := range []struct {
		name  string
		check func(context.Context) error
		code  string
	}{
		{"category rename", func(ctx context.Context) error {
			return CheckEdit(ctx, lib, Edit{ID: category, Name: "Energy", Revision: revision}, true)
		}, ""},
		{"value rename", func(ctx context.Context) error {
			return CheckEdit(ctx, lib, Edit{ID: value, Name: "Driving", Revision: revision}, false)
		}, ""},
		{"stale rename", func(ctx context.Context) error {
			return CheckEdit(ctx, lib, Edit{ID: value, Name: "Driving", Revision: "stale"}, false)
		}, "tag_revision_stale"},
		{"invalid name", func(ctx context.Context) error {
			return CheckEdit(ctx, lib, Edit{ID: category, Name: "\n", Revision: revision}, true)
		}, "tag_name_invalid"},
		{"assignment", func(ctx context.Context) error {
			return CheckAssignment(ctx, engine, lib, AssignmentEdit{Track: "1", Values: []string{value}, Revision: revision})
		}, ""},
		{"missing selected value", func(ctx context.Context) error {
			return CheckAssignment(ctx, engine, lib, AssignmentEdit{Track: "1", Values: []string{"missing"}, Revision: revision})
		}, "tag_not_found"},
		{"stale assignment", func(ctx context.Context) error {
			return CheckAssignment(ctx, engine, lib, AssignmentEdit{Track: "1", Values: []string{value}, Revision: "stale"})
		}, "tag_revision_stale"},
		{"delete preview", func(ctx context.Context) error {
			return CheckDelete(ctx, lib, DeleteOptions{ID: value, Revision: revision, DryRun: true}, false)
		}, ""},
		{"reviewed delete", func(ctx context.Context) error {
			return CheckDelete(ctx, lib, DeleteOptions{ID: value, Revision: revision, PlanToken: preview.PlanToken}, false)
		}, ""},
		{"unreviewed delete", func(ctx context.Context) error {
			return CheckDelete(ctx, lib, DeleteOptions{ID: value, Revision: revision}, false)
		}, "tag_plan_stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.check(t.Context())
			if test.code != "" {
				requireCode(t, err, test.code)
			} else if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, tagBytes(t, lib)) || currentRevision(t, lib) != revision {
				t.Fatal("preflight changed the stored taxonomy or assignments")
			}
		})
	}
}
