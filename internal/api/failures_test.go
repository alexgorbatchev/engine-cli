package api

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestJobFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"permission", &fs.PathError{Op: "open", Path: "/private/library", Err: fs.ErrPermission}, "permission_denied"},
		{"missing_file", fs.ErrNotExist, "resource_not_found"},
		{"timeout", context.DeadlineExceeded, "timed_out"},
		{"unknown", errors.New("SQL internal detail"), "operation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testAPI(t)
			s.operations["test.failure"] = define("test.failure", false, nil, func(context.Context, emptyParams) (any, error) { return nil, tc.err })
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "test.failure", `{}`))
			failure := job["error"].(map[string]any)
			progress := job["progress"].(map[string]any)
			if failure["code"] != tc.code || progress["error_code"] != tc.code {
				t.Fatalf("failure=%v progress=%v, want code %s", failure, progress, tc.code)
			}
		})
	}
}

func TestMissingLibraryHasSpecificCode(t *testing.T) {
	s, lib := testAPI(t)
	path := filepath.Join(lib, "Database2", "m.db")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"track.list", "playlist.list"} {
		job := awaitAPIJob(t, s, submitAPIJob(t, s, operation, `{}`))
		if job["error"].(map[string]any)["code"] != "library_not_found" {
			t.Fatalf("%s: %v", operation, job)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing database was created: %v", err)
	}
}
