//go:build !windows

package hook

import "syscall"

// noFollow keeps the engine from following a link an agent left in the
// queue in place of a file.
const noFollow = syscall.O_NOFOLLOW
