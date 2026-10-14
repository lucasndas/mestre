//go:build windows

package classes

import (
	"os"
	"syscall"
)

// openScript opens a file of the worktree for reading without following a
// link or a junction (Windows has no FIFO in the file system): the path is
// already resolved, so a reparse point there is one swapped in since.
func openScript(p string) (*os.File, error) {
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(ptr, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	f := os.NewFile(uintptr(h), p)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: p, Err: errNotScript}
	}
	return f, nil
}
