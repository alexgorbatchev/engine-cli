package progress

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestErrorCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, ""},
		{"missing", &fs.PathError{Op: "open", Path: "private", Err: fs.ErrNotExist}, "resource_not_found"},
		{"permission", fmt.Errorf("read: %w", fs.ErrPermission), "permission_denied"},
		{"cancelled", context.Canceled, "cancelled"},
		{"timeout", context.DeadlineExceeded, "timed_out"},
		{"missing_tool", &exec.Error{Name: "missing", Err: exec.ErrNotFound}, "tool_unavailable"},
		{"tool_exit", &exec.ExitError{}, "tool_failed"},
		{"guard", &systemcheck.Error{Code: "engine_desktop_running"}, "engine_desktop_running"},
		{"coded", &Error{Code: "library_not_found", Cause: fs.ErrNotExist}, "library_not_found"},
		{"unknown", errors.New("private SQL text"), "operation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ErrorCode(tc.err); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	err := &Error{Code: "library_not_found", Cause: fs.ErrNotExist}
	if !errors.Is(err, fs.ErrNotExist) || err.Error() != fs.ErrNotExist.Error() {
		t.Fatal("lost underlying cause")
	}
}

func TestEmitFailureAlwaysHasCode(t *testing.T) {
	var got Event
	ctx := WithReporter(context.Background(), func(e Event) { got = e })
	Emit(ctx, Event{Type: "error", Error: "private detail"})
	if got.ErrorCode != "operation_failed" {
		t.Fatalf("uncoded event: %+v", got)
	}
}
