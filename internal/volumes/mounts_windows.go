package volumes

import (
	"golang.org/x/sys/windows"
)

// mounted lists fixed and removable drive letters with their labels.
func mounted() ([]mount, error) {
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	var mounts []mount
	for i := uint32(0); i < 26; i++ {
		if drives&(1<<i) == 0 {
			continue
		}
		path := string(rune('A'+i)) + ":\\"
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		kind := windows.GetDriveType(ptr)
		if kind != windows.DRIVE_FIXED && kind != windows.DRIVE_REMOVABLE {
			continue
		}
		name := path[:2]
		var label [windows.MAX_PATH + 1]uint16
		// A drive may disappear while listing; its letter still names it.
		if err := windows.GetVolumeInformation(ptr, &label[0], uint32(len(label)), nil, nil, nil, nil, 0); err == nil {
			if text := windows.UTF16ToString(label[:]); text != "" {
				name = text + " (" + path[:2] + ")"
			}
		}
		mounts = append(mounts, mount{path: path, name: name, removable: kind == windows.DRIVE_REMOVABLE})
	}
	return mounts, nil
}
