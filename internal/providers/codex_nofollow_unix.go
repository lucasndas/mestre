//go:build !windows

package providers

import "syscall"

// transcriptNoFollow refuses a link left in place of the transcript after
// the check of the path.
const transcriptNoFollow = syscall.O_NOFOLLOW
