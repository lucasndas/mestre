//go:build windows

package safefile

import "errors"

// ErrUntrusted: on Windows the ACL of the folder decides, and ReadTrusted is Read.
var ErrUntrusted = errors.New("not made by the engine, or writable by the group or the others")

// ReadTrusted is Read on Windows.
func ReadTrusted(path string, limit int64) ([]byte, Info, error) { return Read(path, limit) }
