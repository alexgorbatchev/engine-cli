package track

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

func defaultUpgradeRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	data, err := defaultRunner(ctx, name, args...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// External tools can include panic dumps and Python tracebacks on stderr.
		return nil, fmt.Errorf("%s failed: %w; run %s separately for details", name, exitErr, name)
	}
	return data, err
}
