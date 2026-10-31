package staging

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type manifestRename struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func publishManifest(root *os.Root, id, tmp string) (err error) {
	// Keep both names relative to the retained, confined parent. An absolute
	// MoveFileEx path would lose os.Root's protection against parent replacement.
	dir, err := root.Open(id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	defer runtime.KeepAlive(dir)
	name, err := windows.NewNTUnicodeString(filepath.Base(tmp))
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(dir.Fd()),
		ObjectName:    name,
		Attributes:    windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	// WRITE_THROUGH applies to the rename on this same handle, including NTFS
	// rename metadata. Flushing a read-only directory handle is not supported.
	err = windows.NtCreateFile(&handle, windows.DELETE|windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_WRITE_THROUGH|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return &os.PathError{Op: "open staging publication", Path: tmp, Err: err}
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(handle)) }()
	// OPEN_REPARSE_POINT prevents following the source, but can open the link
	// itself. Inspect the retained handle before allowing it to replace a manifest.
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return &os.PathError{Op: "inspect staging publication", Path: tmp, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return &os.PathError{Op: "publish staging reparse point", Path: tmp, Err: os.ErrInvalid}
	}
	target := utf16.Encode([]rune(manifestName))
	// The native contract requires sizeof(FILE_RENAME_INFORMATION) plus the
	// name bytes, including the header's trailing alignment padding.
	buffer := make([]byte, int(unsafe.Sizeof(manifestRename{}))+len(target)*2)
	rename := (*manifestRename)(unsafe.Pointer(&buffer[0]))
	rename.ReplaceIfExists = 1
	rename.RootDirectory = windows.Handle(dir.Fd())
	rename.FileNameLength = uint32(len(target) * 2)
	copy(unsafe.Slice(&rename.FileName[0], len(target)), target)
	err = windows.NtSetInformationFile(handle, &status, &buffer[0],
		uint32(len(buffer)), windows.FileRenameInformation)
	if err != nil {
		return &os.LinkError{Op: "publish staging manifest", Old: tmp, New: filepath.Join(id, manifestName), Err: err}
	}
	return nil
}
