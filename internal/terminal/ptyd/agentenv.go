package ptyd

import (
	"fmt"
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
// which stand for the current folder), and the start folder (the worktree)
// or anything under it. Any other system returns env as it is (a nil env
// stands for the environment of the mestre-pty). Fail closed: a PATH that
// ends up empty stays empty, not "everything".
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
// (see canon). An empty dir has nothing under it. Short (8.3) names and
// junctions are not resolved here.
func under(p, dir string) bool {
	pn, dn := canon(p), canon(dir)
	if dn == "" {
		return false
	}
	return pn == dn || strings.HasPrefix(pn, dn+`\`)
}

// canon is the comparison form of a Windows path: lower case, one kind of
// slash, "." and ".." resolved, the \\?\ prefix dropped, trailing dots and
// blanks of a name ignored (Windows ignores them), a UNC path marked.
func canon(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	p = strings.TrimPrefix(p, `\\?\`)
	if rest, ok := strings.CutPrefix(strings.ToLower(p), `unc\`); ok {
		p = `\\` + rest
	}
	prefix := ""
	if strings.HasPrefix(p, `\\`) {
		prefix = `\\`
	}
	var out []string
	for _, seg := range strings.Split(p, `\`) {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		if seg = strings.TrimRight(seg, ". "); seg != "" {
			out = append(out, strings.ToLower(seg))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return prefix + strings.Join(out, `\`)
}

// resolveProgram is the path of the program the mestre-pty starts. On
// Windows a bare name ("claude", "cmd") is looked up in the PATH of env (the
// cleaned one) and never in the start folder, where the library that starts
// the process would look first; a name with a folder part that is not
// absolute is refused. exists says whether a file is there. Any other
// system returns path as it is.
func resolveProgram(goos, path string, env []string, exists func(string) bool) (string, error) {
	if goos != "windows" || isAbs(path) {
		return path, nil
	}
	if path == "" || strings.ContainsAny(path, `\/:`) {
		return "", fmt.Errorf("the program %q is not an absolute path nor a bare name", path)
	}
	var pathVar, pathExt string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(k, "PATH"):
			pathVar = v
		case strings.EqualFold(k, "PATHEXT"):
			pathExt = v
		}
	}
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}
	names := []string{path}
	if !strings.Contains(path, ".") {
		names = nil
		for _, ext := range strings.Split(pathExt, ";") {
			if ext = strings.TrimSpace(ext); ext != "" {
				names = append(names, path+ext)
			}
		}
	}
	for _, dir := range strings.Split(pathVar, ";") {
		if dir == "" {
			continue
		}
		for _, n := range names {
			if full := strings.TrimRight(dir, `\/`) + `\` + n; exists(full) {
				return full, nil
			}
		}
	}
	return "", fmt.Errorf("the program %q is not in the PATH of the agent", path)
}
