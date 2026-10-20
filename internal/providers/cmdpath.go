package providers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// cmdBare is a path that goes bare, without quotes, in a line of cmd: no
// blank and none of & | < > ^ ( ) % ! , ; = " ' /.
var cmdBare = regexp.MustCompile(`^[A-Za-z0-9_.~:\\-]+$`)

// cmdRooted is a Windows path that names its drive (X:\) or its share
// (\\server\share): no path relative to the drive or the folder of cmd.
var cmdRooted = regexp.MustCompile(`^(?:[A-Za-z]:\\|\\\\[^\\]+\\[^\\]+)`)

// plainWindowsPath refuses what a rooted path can still be besides a file in
// a folder: the device and extended namespaces (\\.\ and \\?\, as
// \\.\GLOBALROOT\), a colon after the drive letter (an alternate data
// stream, C:\a.exe:x) and a .. segment.
func plainWindowsPath(p string) bool {
	if strings.HasPrefix(p, `\\.\`) || strings.HasPrefix(p, `\\?\`) {
		return false
	}
	rest := p
	if len(p) >= 2 && p[1] == ':' {
		rest = p[2:]
	}
	if strings.Contains(rest, ":") {
		return false
	}
	return !slices.Contains(strings.Split(p, `\`), "..")
}

// cmdBarePath is binary in the form a line of cmd takes without quotes,
// with backslashes: as it is, or by its 8.3 name on this machine (only on
// Windows, and only where the volume keeps short names). It must name its
// drive or share. Any other path is an error: it cannot go in the hook line
// of Antigravity, whose quotes agy breaks.
func cmdBarePath(binary string) (string, error) {
	p := strings.ReplaceAll(binary, "/", `\`)
	if !cmdRooted.MatchString(p) || !plainWindowsPath(p) {
		return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q does not name its drive (X:\\) or share (\\\\server\\share), or it is a device path, an alternate stream or has a .. segment", binary)
	}
	if cmdBare.MatchString(p) {
		return p, nil
	}
	if s, ok := shortPath(p); ok && cmdBare.MatchString(s) && cmdRooted.MatchString(s) && plainWindowsPath(s) {
		return s, nil
	}
	return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q needs quotes in cmd, which agy breaks, and has no 8.3 name without them: the mestre must be in a folder without blanks and without & | < > ^ ( ) %% ! , ; = ' (docs/provedores/antigravity.md#hooks)", binary)
}
