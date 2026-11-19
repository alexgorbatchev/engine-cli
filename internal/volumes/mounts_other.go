//go:build !darwin && !windows

package volumes

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

func mounted() ([]mount, error) {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return linuxMounts(f)
}

// linuxMounts keeps the root and drives mounted where desktops attach them.
func linuxMounts(r io.Reader) ([]mount, error) {
	var mounts []mount
	seen := map[string]bool{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		path := unescapeMount(fields[1])
		if seen[path] {
			continue
		}
		switch {
		case path == "/":
			mounts = append(mounts, mount{path: path, name: "This computer"})
		case strings.HasPrefix(path, "/media/") || strings.HasPrefix(path, "/run/media/") || strings.HasPrefix(path, "/mnt/"):
			mounts = append(mounts, mount{path: path, removable: true})
		default:
			continue
		}
		seen[path] = true
	}
	return mounts, scanner.Err()
}

// unescapeMount decodes the octal escapes /proc/mounts uses for spaces and tabs.
func unescapeMount(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+3 < len(value) {
			if n, err := strconv.ParseUint(value[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		out.WriteByte(value[i])
	}
	return out.String()
}
