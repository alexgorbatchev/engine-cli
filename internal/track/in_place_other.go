//go:build !linux && !darwin

package track

import "os"

// keepOwner is a no-op where files carry no POSIX owner; the replacement
// inherits the folder's access control as the original did.
func keepOwner(string, os.FileInfo) error { return nil }
