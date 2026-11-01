package fixturecapture

import (
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func invalidInput(format string, args ...any) error {
	return &progress.Error{Code: "fixture_capture_invalid_input", Cause: fmt.Errorf(format, args...)}
}

func inputChanged(message string) error {
	return &progress.Error{Code: "fixture_capture_input_changed", Cause: fmt.Errorf("%s", message)}
}
