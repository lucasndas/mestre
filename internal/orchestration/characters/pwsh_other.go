//go:build !windows

package characters

// systemPowerShell is the PowerShell of a Windows system, by its full path (the
// engine of another system never runs a line for one; the path is what the line
// would hold).
func systemPowerShell() string {
	return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
}
