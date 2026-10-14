package conformance

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// noLinkPath is root joined with parts, each one plain name (no separator,
// not "." or ".."), when every one of them exists and none is a link: a
// symbolic link, or, on Windows, a junction or another reparse point
// (which Go reports as ModeIrregular); every one but the last is a folder.
// The folders of the profile belong to the agent account, which could plant
// a link to send a removal of the cleanup elsewhere.
func noLinkPath(root string, parts ...string) (string, error) {
	at := root
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("%q is not a plain name", part)
		}
		at = filepath.Join(at, part)
		st, err := os.Lstat(at)
		if err != nil {
			return "", err
		}
		switch m := st.Mode(); {
		case m&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			return "", fmt.Errorf("%s is a link", at)
		case i < len(parts)-1 && !m.IsDir():
			return "", fmt.Errorf("%s is not a folder", at)
		}
	}
	return at, nil
}
