//go:build !darwin && !linux && !windows

package transfer

import "fmt"

func availableSpace(string) (uint64, error) {
	return 0, fmt.Errorf("sync space checks require macOS, Windows, or Linux")
}
