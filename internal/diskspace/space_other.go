//go:build !darwin && !linux && !windows

package diskspace

import "fmt"

func Available(string) (uint64, error) {
	return 0, fmt.Errorf("free space checks require macOS, Windows, or Linux")
}
