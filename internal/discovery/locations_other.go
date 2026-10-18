//go:build !darwin && !windows

package discovery

func defaultLocations() ([]location, []Warning) {
	roots, warnings := homeLocations()
	return roots, append(warnings, Warning{Message: "automatic attached-drive discovery is available on macOS and Windows; provide search paths on this system"})
}
