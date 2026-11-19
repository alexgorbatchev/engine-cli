//go:build windows

package playlist

import (
	"path/filepath"
	"strings"
)

// sameVolume reports whether two paths share a drive or UNC share.
func sameVolume(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}
