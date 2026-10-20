package systemcheck

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func listProcesses(ctx context.Context) (result []process, err error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(h, &entry)
	for err == nil {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, process{int(entry.ProcessID), windows.UTF16ToString(entry.ExeFile[:])})
		err = windows.Process32Next(h, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		err = nil
	}
	return result, err
}
