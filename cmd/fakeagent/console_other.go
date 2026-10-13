//go:build !windows && !linux

package main

// rawConsole does nothing on systems other than Linux and Windows, the only
// ones with a PTY in the terminal (docs/arquitetura/terminal.md).
func rawConsole() (restore func()) { return func() {} }
