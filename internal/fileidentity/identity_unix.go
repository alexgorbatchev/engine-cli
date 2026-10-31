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

// IdentityFile observes the retained descriptor, independently of its pathname.
func IdentityFile(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return Identity("", info)
}
