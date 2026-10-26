//go:build !windows

package attach

import "os"

// enableVT is for Windows: a Unix terminal already speaks VT.
func enableVT(_, _ *os.File) func() { return func() {} }
