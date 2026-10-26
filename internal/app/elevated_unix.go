//go:build !windows

package app

import "os"

func isElevated() bool { return os.Geteuid() == 0 }
