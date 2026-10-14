package hook

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openQueued opens a file of the queue for the engine to read, on the file
// itself and never on what a reparse point (a symbolic link, a junction)
// names, and refuses a reparse point and a file with more than one link: a
// hard link of a file the agent could not read itself.
func openQueued(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		_ = windows.CloseHandle(h)
		return nil, errors.New("a reparse point, not a file")
	case info.NumberOfLinks > 1:
		_ = windows.CloseHandle(h)
		return nil, errors.New("a file with more than one link")
	}
	return os.NewFile(uintptr(h), path), nil
}
