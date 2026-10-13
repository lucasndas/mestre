//go:build spike && windows

package dbcapture

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenShareDelete opens path for reading with FILE_SHARE_DELETE, which
// os.Open does not grant: SQLite can then delete the -wal while the handle
// is open (the delete stays pending until the handle closes).
func OpenShareDelete(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
