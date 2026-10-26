package panel

import (
	"embed"
	"io/fs"
)

// The build of the front end (web/) goes to webdist/, which is outside git except
// for the markers; "all:" embeds them, so `go build` works without Node.
//
//go:embed all:webdist
var distFS embed.FS

// Assets are the static files of the panel as built: the root of webdist/.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "webdist")
	if err != nil {
		return distFS // unreachable: the folder is embedded above
	}
	return sub
}
