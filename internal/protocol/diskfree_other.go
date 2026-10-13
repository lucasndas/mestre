//go:build !linux && !windows

package protocol

import "errors"

// diskFree is not known on the systems the Mestre does not run on; the
// check of the space is skipped.
func diskFree(string) (uint64, error) { return 0, errors.ErrUnsupported }
