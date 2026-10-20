//go:build !windows

package localchan

import "github.com/lucasndas/mestre/internal/terminal"

// DefaultTerminalAccess is nil outside Windows: the group and the mode of
// the socket already cover every terminal (DefaultAccess).
func DefaultTerminalAccess(string) terminal.Access { return nil }
