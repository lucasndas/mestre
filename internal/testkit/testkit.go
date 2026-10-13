// Package testkit holds the shared test doubles (docs/qualidade/dubles.md):
// recordings, the secret scan of the recordings, the "not implemented"
// rule, temporary files and ports. Production code must not import it
// (depguard in .golangci.yml).
package testkit

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

// all: so a file starting with "." or "_" is embedded too, and the secret scan
// of the recordings (CA-DUBLES-10) sees it.
//
//go:embed all:testdata/recordings
var recordingsFS embed.FS

// Recordings returns the recordings tree (origin/version/file.json).
func Recordings() fs.FS {
	sub, err := fs.Sub(recordingsFS, "testdata/recordings")
	if err != nil {
		panic(err) // embedded path is fixed at build time
	}
	return sub
}

// Recording is one recorded event or response.
type Recording struct {
	Origin     string          `json:"origin"`
	Version    string          `json:"version"`
	CapturedAt string          `json:"captured_at"`
	Source     string          `json:"source"`
	Body       json.RawMessage `json:"body"`
}

// LatestVersion returns the newest version directory of origin. Versions are
// compared part by part: numerically when both parts are numbers, a number
// before a name otherwise (a numbered release is newer than a version known
// only by its commit, like the Codex recording "b3c3bad"), and by text when
// both are names.
func LatestVersion(fsys fs.FS, origin string) (string, error) {
	entries, err := fs.ReadDir(fsys, origin)
	if err != nil {
		return "", fmt.Errorf("recordings of %s: %w", origin, err)
	}
	var vs []string
	for _, e := range entries {
		if e.IsDir() {
			vs = append(vs, e.Name())
		}
	}
	if len(vs) == 0 {
		return "", fmt.Errorf("recordings of %s: no version", origin)
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs[len(vs)-1], nil
}

// LoadRecording reads origin/version/name.json; an empty version means the
// newest one.
func LoadRecording(fsys fs.FS, origin, version, name string) (Recording, error) {
	if version == "" {
		v, err := LatestVersion(fsys, origin)
		if err != nil {
			return Recording{}, err
		}
		version = v
	}
	p := path.Join(origin, version, name+".json")
	b, err := fs.ReadFile(fsys, p)
	if err != nil {
		return Recording{}, fmt.Errorf("recording %s: %w", p, err)
	}
	var r Recording
	if err := json.Unmarshal(b, &r); err != nil {
		return Recording{}, fmt.Errorf("recording %s: %w", p, err)
	}
	if r.Version == "" || r.CapturedAt == "" || len(r.Body) == 0 {
		return Recording{}, fmt.Errorf("recording %s: missing version, captured_at or body", p)
	}
	return r, nil
}

func versionLess(a, b string) bool {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil:
			return na < nb
		case ea == nil: // a number beats a name: "0.1.0" is newer than a commit id
			return false
		case eb == nil:
			return true
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}

// Reporter is the part of testing.TB a double uses to fail the test.
type Reporter interface {
	Errorf(format string, args ...any)
}

// NotImplementedPrefix starts the failure of a double asked for something
// it does not implement (docs/qualidade/dubles.md#regras-e-erros).
const NotImplementedPrefix = "not implemented in double: "

// NotImplemented fails the test through rep and answers 501, never an empty
// success.
func NotImplemented(rep Reporter, w http.ResponseWriter, route string) {
	msg := NotImplementedPrefix + route
	rep.Errorf("%s", msg)
	http.Error(w, msg, http.StatusNotImplemented)
}
