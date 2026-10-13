package machinesheet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Runner runs a program to read its version and returns what it printed. The
// engine wires one over the layers that start processes (internal/gitx for
// git, the launcher for the rest); the packages that read the sheet start no
// process, and the tests use a fake.
type Runner interface {
	Run(ctx context.Context, exe string, args ...string) (string, error)
}

var versionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// firstVersion is the first dotted number in the output of `--version`.
func firstVersion(out string) string { return versionRe.FindString(out) }

// lookPath finds a program on the PATH given, the way the system would: a file
// with an execute bit on Linux, a file with one of the PATHEXT suffixes on
// Windows. It starts nothing.
func lookPath(name, pathEnv, goos, pathExt string) string {
	exts := []string{""}
	if goos == OSWindows {
		exts = nil
		for _, e := range strings.Split(pathExt, ";") {
			if e != "" {
				exts = append(exts, e)
			}
		}
		if len(exts) == 0 {
			exts = []string{".exe", ".cmd", ".bat", ".com"}
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(dir, name+ext)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && (goos == OSWindows || st.Mode()&0o111 != 0) {
				return p
			}
		}
	}
	return ""
}

type toolsProbe struct {
	runner Runner
	names  []string
	// path replaces the PATH of the process (tests).
	path *string
}

// NewToolsProbe finds the tools on the PATH and reads their versions with
// `<tool> --version`. A tool that is not installed is left out. Without a
// Runner a tool that is installed is listed with an empty version.
func NewToolsProbe(r Runner, names ...string) ToolsProbe {
	return toolsProbe{runner: r, names: names}
}

// Tools implements ToolsProbe.
func (t toolsProbe) Tools(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	path := os.Getenv("PATH")
	if t.path != nil {
		path = *t.path
	}
	for _, n := range t.names {
		exe := lookPath(n, path, runtime.GOOS, os.Getenv("PATHEXT"))
		if exe == "" {
			continue
		}
		out[n] = ""
		if t.runner == nil {
			continue
		}
		text, err := t.runner.Run(ctx, exe, "--version")
		if err != nil {
			return nil, fmt.Errorf("%s --version: %w", n, err)
		}
		out[n] = firstVersion(text)
	}
	return out, nil
}

// browserProbe finds the browser of the browser MCPs.
type browserProbe struct{ runner Runner }

// NewBrowserProbe finds Chrome (or Chromium, or Edge on Windows). On Windows
// the version comes from the registry; on Linux from `--version`, which
// needs the Runner (without one the version is empty).
func NewBrowserProbe(r Runner) BrowserProbe { return browserProbe{runner: r} }

// Browser implements BrowserProbe.
func (b browserProbe) Browser(ctx context.Context) (*Browser, error) {
	return detectBrowser(ctx, b.runner)
}
