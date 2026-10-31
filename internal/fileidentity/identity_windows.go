package fileidentity

import (
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Identity eagerly captures the native physical file identity from an observation.
func Identity(path string, _ os.FileInfo) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	return identityHandle(handle)
}

// IdentityFile observes the retained descriptor, independently of its pathname.
func IdentityFile(file *os.File) (string, error) {
	id, err := identityHandle(windows.Handle(file.Fd()))
	runtime.KeepAlive(file)
	return id, err
}

func identityHandle(handle windows.Handle) (string, error) {
	// FILE_ID_INFO contains the volume serial followed by the full 128-bit file ID, including on ReFS.
	var info struct {
		VolumeSerialNumber uint64
		FileID             [16]byte
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x:%s", info.VolumeSerialNumber, hex.EncodeToString(info.FileID[:])), nil
}
