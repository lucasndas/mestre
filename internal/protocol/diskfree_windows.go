//go:build windows

package protocol

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// diskFree is what the disk of dir has free for this user.
func diskFree(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	return avail, nil
}
