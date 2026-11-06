package intakelease

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func lockReservation(file *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
	runtime.KeepAlive(file)
	return err
}

func nativeReservationBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func matchingReservationNames(parent, anchors *os.Root) error {
	read := func(root *os.Root) (_ uint32, err error) {
		file, err := root.Open(".")
		if err != nil {
			return 0, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		var capabilities uint32
		if err := windows.GetVolumeInformationByHandle(windows.Handle(file.Fd()), nil, 0, nil, nil, &capabilities, nil, 0); err != nil {
			return 0, err
		}
		// A volume without case-sensitive lookup cannot have a conflicting
		// per-directory setting. In particular, FAT/exFAT need not implement
		// the NTFS per-directory information class.
		if capabilities&windows.FILE_CASE_SENSITIVE_SEARCH == 0 {
			runtime.KeepAlive(file)
			return 0, nil
		}
		var flags uint32
		err = windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)))
		runtime.KeepAlive(file)
		return flags, err
	}
	a, err := read(parent)
	if err != nil {
		return err
	}
	b, err := read(anchors)
	if err != nil {
		return err
	}
	if a != b {
		return errors.New("intake path coordination has different case sensitivity from its parent")
	}
	return nil
}
