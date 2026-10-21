//go:build !darwin && !windows

package systemcheck

import "context"

// Engine DJ Desktop does not run natively on these platforms.
func listProcesses(context.Context) ([]process, error) { return nil, nil }
