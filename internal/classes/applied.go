package classes

import (
	"net/url"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// The two checks below serve the registration of the use of a decree after the
// hook let a call through (decrees.Service.Applied): the number of the decree an
// event carries is never trusted alone, so the call the event describes must be
// the one the decree frees. They are looser than the decision of the hook (the
// event has the raw paths, the decision the displayed ones) and never grant
// anything: they only say whether a use is worth recording.

// ActionMatchesTool reports whether the call is the exact action an exception
// binds. A path the exception binds as displayed (relative to the worktree) is
// the call's path or the tail of it.
func ActionMatchesTool(a Action, t *providers.Tool) bool {
	if t == nil || a.Kind != t.Kind {
		return false
	}
	switch a.Kind {
	case providers.KindShell:
		return a.Command != "" && a.Command == strings.TrimSpace(t.Command)
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		if len(a.Paths) == 0 || len(a.Paths) != len(t.Paths) {
			return false
		}
		for _, p := range a.Paths {
			found := false
			for _, q := range t.Paths {
				if pathTail(q, p) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case providers.KindWeb:
		return a.Tool == t.NativeName && a.URL == t.URL
	}
	return a.Tool != "" && a.Tool == t.NativeName
}

func pathTail(full, p string) bool {
	full, p = strings.ReplaceAll(full, "\\", "/"), strings.ReplaceAll(p, "\\", "/")
	return full == p || strings.HasSuffix(full, "/"+strings.TrimPrefix(p, "./"))
}

// GrantCoversTool reports whether what a rule allows covers the call.
func GrantCoversTool(g *Grant, t *providers.Tool) bool {
	if g == nil || t == nil {
		return false
	}
	for _, p := range g.Tools {
		if matchText(p, t.NativeName) {
			return true
		}
	}
	switch t.Kind {
	case providers.KindShell:
		cmd := strings.TrimSpace(t.Command)
		for _, p := range g.Commands {
			if matchText(p, cmd) {
				return true
			}
		}
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		if len(t.Paths) == 0 {
			return false
		}
		for _, q := range t.Paths {
			if !fileCovered(g.Files, strings.ReplaceAll(q, "\\", "/")) {
				return false
			}
		}
		return true
	case providers.KindWeb:
		if u, err := url.Parse(t.URL); err == nil && u.Hostname() != "" {
			for _, d := range g.Domains {
				if matchDomain(d, u.Hostname()) {
					return true
				}
			}
		}
	}
	return false
}

// fileCovered tries the pattern on the path and on each tail of it, because the
// patterns of a rule are relative to the worktree and the event has the path as
// the agent wrote it.
func fileCovered(patterns []string, path string) bool {
	for _, p := range patterns {
		if matchGlob(p, path) {
			return true
		}
		for i := 0; i < len(path); i++ {
			if path[i] == '/' && matchGlob(p, path[i+1:]) {
				return true
			}
		}
	}
	return false
}
