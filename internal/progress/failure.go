package progress

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// Error attaches a stable public classification while preserving the cause.
type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string { return e.Cause.Error() }
func (e *Error) Unwrap() error { return e.Cause }

// ErrorCode classifies typed causes, never diagnostic message text.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	var check *systemcheck.Error
	if errors.As(err, &check) {
		return check.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	case errors.Is(err, exec.ErrNotFound):
		return "tool_unavailable"
	case errors.Is(err, fs.ErrNotExist):
		return "resource_not_found"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "tool_failed"
	}
	return "operation_failed"
}
