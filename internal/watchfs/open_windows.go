package watchfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const directoryAccess = windows.FILE_LIST_DIRECTORY | windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE

const sharing = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

func nativeRoot(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, directoryAccess, sharing, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return acceptHandle(handle, path, true)
}

func nativeChild(parent *os.File, name string, directory bool) (*os.File, error) {
	value, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	conn, err := parent.SyscallConn()
	if err != nil {
		return nil, err
	}
	access := uint32(windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_NON_DIRECTORY_FILE)
	if directory {
		access = directoryAccess
		options = windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_DIRECTORY_FILE
	}
	var handle windows.Handle
	var openErr error
	if err := conn.Control(func(fd uintptr) {
		attributes := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(fd), ObjectName: value, Attributes: windows.OBJ_CASE_INSENSITIVE}
		attributes.Length = uint32(unsafe.Sizeof(attributes))
		var status windows.IO_STATUS_BLOCK
		openErr = windows.NtCreateFile(&handle, access, &attributes, &status, nil, 0, sharing, windows.FILE_OPEN, options, 0, 0)
	}); err != nil {
		return nil, err
	}
	path := filepath.Join(parent.Name(), name)
	if openErr != nil {
		var status windows.NTStatus
		if errors.As(openErr, &status) {
			openErr = status.Errno()
		}
		return nil, &os.PathError{Op: "open watched child", Path: path, Err: openErr}
	}
	return acceptHandle(handle, path, directory)
}

func acceptHandle(handle windows.Handle, path string, directory bool) (*os.File, error) {
	file := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(handle, &info)
	if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = fs.ErrInvalid
	}
	if err == nil {
		var stat os.FileInfo
		stat, err = file.Stat()
		if err == nil && (directory && !stat.IsDir() || !directory && !stat.Mode().IsRegular()) {
			err = fs.ErrInvalid
		}
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
