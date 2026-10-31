//go:build linux || darwin

package performance

import (
	"fmt"
	"os"
	"syscall"
)

func mediaIdentity(_ string, info os.FileInfo) (string, error) {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
