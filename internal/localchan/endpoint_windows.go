package localchan

import (
	"os"
	"path/filepath"
)

func socketPath(instance string) string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "Mestre", instance, "run", "cli.sock")
}
