package localchan

import "path/filepath"

func socketPath(instance string) string {
	return filepath.Join("/run/mestre", instance, "cli.sock")
}
