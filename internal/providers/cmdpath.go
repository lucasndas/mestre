package providers

import (
	"fmt"
	"regexp"
	"strings"
)

// cmdBare is a path that goes bare, without quotes, in a line of cmd: no
// blank and none of & | < > ^ ( ) % ! , ; = " ' /.
var cmdBare = regexp.MustCompile(`^[A-Za-z0-9_.~:\\-]+$`)

// cmdRooted is a Windows path that names its drive (X:\) or its share
// (\\server\share): no path relative to the drive or the folder of cmd.
var cmdRooted = regexp.MustCompile(`^(?:[A-Za-z]:\\|\\\\[^\\]+\\[^\\]+)`)

// cmdBarePath is binary in the form a line of cmd takes without quotes,
// with backslashes: as it is, or by its 8.3 name on this machine (only on
// Windows, and only where the volume keeps short names). It must name its
// drive or share. Any other path is an error: it cannot go in the hook line
// of Antigravity, whose quotes agy breaks.
func cmdBarePath(binary string) (string, error) {
	p := strings.ReplaceAll(binary, "/", `\`)
	if !cmdRooted.MatchString(p) {
		return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q does not name its drive (X:\\) or share (\\\\server\\share)", binary)
	}
	if cmdBare.MatchString(p) {
		return p, nil
	}
	if s, ok := shortPath(p); ok && cmdBare.MatchString(s) && cmdRooted.MatchString(s) {
		return s, nil
	}
	return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q needs quotes in cmd, which agy breaks, and has no 8.3 name without them: the mestre must be in a folder without blanks and without & | < > ^ ( ) %% ! , ; = ' (docs/provedores/antigravity.md#hooks)", binary)
}
