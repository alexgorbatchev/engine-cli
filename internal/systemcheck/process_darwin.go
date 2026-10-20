package systemcheck

import (
	"context"
	"golang.org/x/sys/unix"
)

var processSnapshot = unix.SysctlKinfoProcSlice

func listProcesses(ctx context.Context) ([]process, error) {
	rows, err := processSnapshot("kern.proc.all")
	if err != nil {
		return nil, err
	}
	result := make([]process, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, process{int(row.Proc.P_pid), unix.ByteSliceToString(row.Proc.P_comm[:])})
	}
	return result, nil
}
