package ptyd

import (
	"os"
	"strings"
)

// noCwdInExePath is the variable that stops cmd.exe and CreateProcess from
// looking for a program in the current folder first. The current folder of
// an agent is its worktree, which it writes: a git.cmd or git.exe planted
// there would run in place of the real one, as the agent's account and
// outside the hook (docs/arquitetura/windows.md#programas-do-agente).
const noCwdInExePath = "NoDefaultCurrentDirectoryInExePath"

// agentEnv is the environment an agent process (and the hooks that inherit
// it) gets from the mestre-pty. On Windows it sets
// NoDefaultCurrentDirectoryInExePath=1 and takes out of PATH the entries the
// agent can plant a program in: the empty ones and the relative ones (".",
// which stand for the current folder), and the worktree itself or anything
// under it. Any other system returns env as it is (a nil env stands for the
// environment of the mestre-pty). Fail closed: a PATH that ends up empty
// stays empty, not "everything".
func agentEnv(goos string, env []string, dir string) []string {
	if goos != "windows" {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(k, noCwdInExePath):
			// replaced below
		case strings.EqualFold(k, "PATH"):
			out = append(out, k+"="+cleanPath(v, dir))
		default:
			out = append(out, kv)
		}
	}
	return append(out, noCwdInExePath+"=1")
}

// cleanPath drops from a Windows PATH the empty and relative entries and the
// ones at or under dir.
func cleanPath(path, dir string) string {
	var keep []string
	for _, e := range strings.Split(path, ";") {
		e = strings.TrimSpace(strings.Trim(strings.TrimSpace(e), `"`))
		if e == "" || !isAbs(e) || under(e, dir) {
			continue
		}
		keep = append(keep, e)
	}
	return strings.Join(keep, ";")
}

// isAbs: a drive path (C:\x, C:/x) or a UNC path; "C:x" and "\x" depend on
// the current folder or drive and are not absolute.
func isAbs(p string) bool {
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// under reports whether p is dir or inside it, by the rules of Windows paths
// (case-insensitive, both slashes). An empty dir has nothing under it.
func under(p, dir string) bool {
	if dir == "" {
		return false
	}
	norm := func(s string) string {
		s = strings.ToLower(strings.ReplaceAll(s, "/", `\`))
		return strings.TrimRight(s, `\`)
	}
	pn, dn := norm(p), norm(dir)
	if dn == "" {
		return false
	}
	return pn == dn || strings.HasPrefix(pn, dn+`\`)
}
