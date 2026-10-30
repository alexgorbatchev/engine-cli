package performance

import (
	"encoding/hex"
	"os"

	"golang.org/x/sys/windows"
)

func mediaIdentity(path string, _ os.FileInfo) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	// FILE_ID_INFO contains the volume serial followed by the full 128-bit file ID, including on ReFS.
	var info [24]byte
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, &info[0], uint32(len(info))); err != nil {
		return "", err
	}
	return hex.EncodeToString(info[:]), nil
}
