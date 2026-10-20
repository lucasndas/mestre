package app

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// systemCmd is the command interpreter of the system, by its full path: the
// terminal runs the line with the worktree as the current folder, and a cmd.exe
// planted there (by a branch, by an agent) must not run in its place. The path is
// the engine's, never read from an environment.
func systemCmd() string {
	dir, err := windows.GetSystemDirectory()
	if err != nil || dir == "" {
		dir = `C:\Windows\System32`
	}
	return filepath.Join(dir, "cmd.exe")
}
