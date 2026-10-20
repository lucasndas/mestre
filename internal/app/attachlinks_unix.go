//go:build !windows

package app

import (
	"os"
	"syscall"
)

// hardLinked says a file has more than one name: an agent that could write the
// folder may have linked a file it cannot read itself, and the engine would copy
// its content where the agents read.
func hardLinked(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return true
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Nlink > 1
}
