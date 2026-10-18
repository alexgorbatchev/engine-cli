package discovery

import (
	"errors"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

func defaultLocations() ([]location, []Warning) {
	roots, warnings := homeLocations()
	music, err := musicFolder()
	if err != nil {
		warnings = append(warnings, Warning{Message: "locating Music folder: " + err.Error()})
	} else {
		roots = append([]location{{Path: music, Name: "Local library", Source: "local"}}, roots...)
	}
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		return roots, append(warnings, Warning{Message: "listing attached drives: " + err.Error()})
	}
	for i := uint32(0); i < 26; i++ {
		if drives&(1<<i) == 0 {
			continue
		}
		path := string(rune('A'+i)) + ":\\"
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return roots, append(warnings, Warning{Message: err.Error()})
		}
		kind := windows.GetDriveType(ptr)
		if kind == windows.DRIVE_FIXED || kind == windows.DRIVE_REMOVABLE {
			name := "Drive: " + path[:2]
			var label [windows.MAX_PATH + 1]uint16
			// A drive may disappear during discovery; the letter remains a useful
			// fallback when its optional volume label cannot be read.
			if err := windows.GetVolumeInformation(ptr, &label[0], uint32(len(label)), nil, nil, nil, nil, 0); err == nil {
				if text := windows.UTF16ToString(label[:]); text != "" {
					name = "Drive: " + text + " (" + path[:2] + ")"
				}
			}
			roots = append(roots, location{Path: filepath.Clean(path), Name: name, Source: "drive"})
		}
	}
	return roots, warnings
}

func musicFolder() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return "", err
	}
	defer windows.CoUninitialize()
	return windows.KnownFolderPath(windows.FOLDERID_Music, windows.KF_FLAG_DONT_VERIFY)
}
