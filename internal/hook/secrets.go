package hook

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/providers"
)

// A key written into a file (docs/classes/permissoes.md#regras-e-erros, "Chave
// escrita em arquivo"): the pre_tool of a write whose content carries a secret
// of a format of the masker is denied, and a write that went through with one
// (the check of the engine did not see it: a tool the rules did not look at, or a secret the vault opened between the two) is told to the agent after the
// tool ran. The message names the place and the kind, never the value.

// secretFinding is a line of the content of a write that carries a secret.
type secretFinding struct {
	path string
	line int
	kind string
}

func (f secretFinding) String() string { return fmt.Sprintf("%s:%d %s", f.path, f.line, f.kind) }

// content is a piece of text a tool writes, for the file it writes it to.
type content struct {
	path string
	text string
	// first is the line of the file the text starts at, minus one.
	first int
}

// notContent are the arguments of a file tool that are not what it writes: the
// paths, the text it replaces (a key in it is going away) and the notes of the
// provider.
var notContent = map[string]bool{
	"file_path": true, "notebook_path": true, "path": true, "old_string": true, "old_source": true,
	"TargetFile": true, "AbsolutePath": true, "TargetContent": true, "Description": true,
	"toolAction": true, "toolSummary": true, "Cwd": true,
}

// writtenSecrets are the secrets in what the tool of the event writes, in the
// order of the file; worktree makes the paths relative to it.
func writtenSecrets(m *logging.Masker, ev providers.Event, worktree string) []secretFinding {
	if ev.Tool == nil {
		return nil
	}
	var out []secretFinding
	for _, c := range writtenContent(ev.Tool) {
		for i, line := range strings.Split(c.text, "\n") {
			kind := logging.SecretKind(line)
			if m != nil {
				kind = m.SecretKind(line)
			}
			if kind != "" {
				out = append(out, secretFinding{path: displayPath(c.path, worktree), line: c.first + i + 1, kind: kind})
			}
		}
	}
	return out
}

// displayPath is the path as the agent knows it: relative to its worktree when
// it is inside, with slashes.
func displayPath(p, worktree string) string {
	if p == "" {
		return "(sem arquivo)"
	}
	if worktree != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(worktree, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	return filepath.ToSlash(p)
}

// writtenContent is the text the tool writes: the content of a file tool (any
// string of its input but the paths and the text being replaced; a patch of
// Codex is read by file) or what a shell command writes by a here-document, a
// redirection, tee or sed -i.
func writtenContent(t *providers.Tool) []content {
	switch t.Kind {
	case providers.KindWrite, providers.KindEdit:
		path := ""
		if len(t.Paths) > 0 {
			path = t.Paths[0]
		}
		var in any
		if json.Unmarshal(t.RawInput, &in) != nil {
			return nil
		}
		if patch := patchOf(in); patch != "" {
			return patchContent(patch, t.Paths)
		}
		var out []content
		for _, s := range stringLeaves(in) {
			out = append(out, content{path: path, text: s})
		}
		return out
	case providers.KindShell:
		return shellContent(t.Command)
	}
	return nil
}

// stringLeaves are the strings of a tool input that are content.
func stringLeaves(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, stringLeaves(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for k, e := range x {
			if !notContent[k] {
				out = append(out, stringLeaves(e)...)
			}
		}
		return out
	}
	return nil
}

// patchOf is the patch of an apply_patch input, "" when the input is not one.
func patchOf(in any) string {
	switch x := in.(type) {
	case string:
		if strings.HasPrefix(strings.TrimSpace(x), "*** Begin Patch") {
			return x
		}
	case map[string]any:
		for _, k := range []string{"command", "input", "patch"} {
			if s, ok := x[k].(string); ok && strings.HasPrefix(strings.TrimSpace(s), "*** Begin Patch") {
				return s
			}
		}
	}
	return ""
}

var patchFile = regexp.MustCompile(`^\*\*\* (?:Add|Update) File: (.+?)\s*$`)

// patchContent is the lines a patch adds, by file; the line is the one among the
// lines the patch adds to the file (exact for a new file).
func patchContent(patch string, paths []string) []content {
	var out []content
	cur := -1
	for _, l := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if m := patchFile.FindStringSubmatch(l); m != nil {
			p := m[1]
			for _, full := range paths {
				if filepath.Base(full) == filepath.Base(p) && strings.HasSuffix(filepath.ToSlash(full), strings.TrimPrefix(filepath.ToSlash(p), "./")) {
					p = full
				}
			}
			out = append(out, content{path: p})
			cur = len(out) - 1
			continue
		}
		if cur >= 0 && strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			if out[cur].text != "" {
				out[cur].text += "\n"
			}
			out[cur].text += l[1:]
		}
	}
	return out
}

var (
	heredocOpen   = regexp.MustCompile(`<<-?\s*['"]?[A-Za-z_][A-Za-z0-9_]*['"]?`)
	redirectInto  = regexp.MustCompile(`(?:^|[\s;|&(])[0-9]?>>?[ \t]*("[^"\n]+"|'[^'\n]+'|[^\s|&;<>()"']+)`)
	teeInto       = regexp.MustCompile(`\btee\s+(?:-[A-Za-z-]+\s+)*("[^"\n]+"|'[^'\n]+'|[^\s|&;<>()"']+)`)
	sedInPlace    = regexp.MustCompile(`\bsed\s(?:[^|;&\n]*?\s)?(?:-[A-Za-z]*i[A-Za-z.]*|--in-place(?:=\S*)?)(?:\s|$)`)
	netCommand    = regexp.MustCompile(`^\s*(?:curl|wget|http|https|git|gh)\b`)
	statements    = regexp.MustCompile(`&&|\|\||;`)
	notAFileLikes = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "NUL": true, "nul": true}
)

// shellContent is the text a shell command writes into a file: the whole command
// when it has a target (a redirection, tee, sed -i), with the lines of a
// here-document counted from the first one. A network command is not one that
// writes what it says (its output goes to the file).
func shellContent(cmd string) []content {
	if cmd == "" {
		return nil
	}
	hd := heredocOpen.FindStringIndex(cmd)
	if hd == nil {
		// without a here-document each statement is judged alone, so a network
		// command does not hide a write that follows it
		var out []content
		for _, seg := range statements.Split(cmd, -1) {
			if !netCommand.MatchString(seg) {
				out = append(out, shellWrite(seg, -1)...)
			}
		}
		return out
	}
	return shellWrite(cmd, hd[0])
}

// shellWrite is what one piece of a command writes: the text, for the first
// target that is a real file (a redirection, tee, sed -i). at is where the
// here-document opens, or -1.
func shellWrite(cmd string, at int) []content {
	target := ""
	unquote := func(s string) string { return strings.Trim(s, `"'`) }
	for _, m := range redirectInto.FindAllStringSubmatch(cmd, -1) {
		if t := unquote(m[1]); !notAFileLikes[t] && !strings.HasPrefix(m[1], "&") {
			target = t
			break
		}
	}
	if target == "" {
		if m := teeInto.FindStringSubmatch(cmd); m != nil {
			target = unquote(m[1])
		} else if loc := sedInPlace.FindStringIndex(cmd); loc != nil {
			seg, _, _ := strings.Cut(cmd[loc[0]:], "\n")
			if f := strings.Fields(seg); len(f) > 1 {
				target = unquote(f[len(f)-1])
			}
		}
	}
	if target == "" {
		return nil
	}
	if at >= 0 {
		return heredocContent(cmd, target, at)
	}
	return []content{{path: target, text: cmd}}
}

// heredocContent splits the command at the line of the here-document: the line
// of the <<, whose lines count from its own, and the body, counted from 1.
func heredocContent(cmd, target string, at int) []content {
	head := strings.Count(cmd[:at], "\n")
	lines := strings.Split(cmd, "\n")
	var out []content
	for i, l := range lines {
		switch {
		case i <= head:
			out = append(out, content{path: target, text: l, first: i})
		default:
			out = append(out, content{path: target, text: l, first: i - head - 1})
		}
	}
	return out
}
