//go:build !windows

package main

// rawConsole does nothing outside Windows: until the PTY of M1 the launcher
// connects the fake agent by pipes, which have no line discipline.
func rawConsole() (restore func()) { return func() {} }
