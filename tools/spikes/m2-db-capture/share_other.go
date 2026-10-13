//go:build spike && !windows

package dbcapture

import "os"

// OpenShareDelete is os.Open outside Windows, where sharing modes do not
// exist.
func OpenShareDelete(path string) (*os.File, error) { return os.Open(path) }
