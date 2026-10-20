package providers

import (
	"fmt"
	"regexp"
)

// cmdBare is a path that goes bare, without quotes, in a line of cmd: no
// blank and none of & | < > ^ ( ) % ! , ; = " '.
var cmdBare = regexp.MustCompile(`^[A-Za-z0-9_.~:\\/-]+$`)

// cmdBarePath is binary in the form a line of cmd takes without quotes: as
// it is, or by its 8.3 name on this machine (only on Windows, and only where
// the volume keeps short names). Any other path is an error: it cannot go in
// the hook line of Antigravity, whose quotes agy breaks.
func cmdBarePath(binary string) (string, error) {
	if cmdBare.MatchString(binary) {
		return binary, nil
	}
	if s, ok := shortPath(binary); ok && cmdBare.MatchString(s) {
		return s, nil
	}
	return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q needs quotes in cmd, which agy breaks, and has no 8.3 name without them: the mestre must be in a folder without blanks and without & | < > ^ ( ) %% ! , ; = ' (docs/provedores/antigravity.md#hooks)", binary)
}
