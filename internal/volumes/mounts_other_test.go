//go:build !darwin && !windows

package volumes

import (
	"strings"
	"testing"
)

func TestLinuxMountsKeepRootAndDesktopDrives(t *testing.T) {
	got, err := linuxMounts(strings.NewReader("/dev/sda1 / ext4 rw 0 0\nproc /proc proc rw 0 0\n/dev/sdb1 /media/dj/DJ\\040USB vfat rw 0 0\n/dev/sdb1 /media/dj/DJ\\040USB vfat rw 0 0\nbad\n"))
	if err != nil || len(got) != 2 || got[0].name != "This computer" || got[1].path != "/media/dj/DJ USB" || !got[1].removable {
		t.Fatalf("mounts=%+v err=%v", got, err)
	}
	if got := unescapeMount(`a\x`); got != `a\x` {
		t.Fatalf("unescape=%q", got)
	}
}
