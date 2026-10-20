package characters

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// systemPowerShell is the PowerShell of the system, by its full path: the
// terminal runs the line with the folder of the worktree as the current one, and
// a program of that name planted there (by a branch, by an agent) must not run
// in its place. The path is the engine's, never read from the environment of the agent.
func systemPowerShell() string {
	dir, err := windows.GetSystemDirectory()
	if err != nil || dir == "" {
		dir = `C:\Windows\System32`
	}
	return filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
}
