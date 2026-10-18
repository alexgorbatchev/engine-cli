//go:build !darwin && !windows

package systemcheck

import "context"

func listProcesses(context.Context) ([]process, error) { return nil, errUnsupported }
