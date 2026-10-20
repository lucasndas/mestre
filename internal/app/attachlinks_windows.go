//go:build windows

package app

import "os"

// hardLinked: on Windows the folders of the agents have their own ACL and the
// engine does not look at the number of links.
func hardLinked(*os.File) bool { return false }
