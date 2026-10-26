// Package panel is the HTTP side of the panel of the Citadel: the login and the
// sessions, the API under /api, the channel of the browser (/ws/ui) and the
// static files of the front end (docs/painel/login.md). Only the acting Citadel
// serves it; every other machine answers 307 to the Citadel of now.
//
// Each area of the API registers its routes in a file routes_<area>.go with
// RegisterArea, so a new area touches no shared file. The front end is built
// into webdist/ and embedded; the tests give the files as an fs.FS.
package panel

//go:generate go tool tygo generate --config tygo.yaml
