//go:build !windows

package app

// systemCmd is the command interpreter of a Windows system, by its full path (the
// engine of another system never runs a line for one; the path is what the line
// would hold).
func systemCmd() string { return `C:\Windows\System32\cmd.exe` }
