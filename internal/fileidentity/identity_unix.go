//go:build linux || darwin

package fileidentity

import (
	"fmt"
	"os"
	"syscall"
)

// Identity eagerly captures the native physical file identity from an observation.
func Identity(_ string, info os.FileInfo) (string, error) {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
