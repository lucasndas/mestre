package classes

import (
	"fmt"
	"slices"
	"strings"
)

// Here-documents (docs/classes/formato-das-permissoes.md#comandos). The
// body of a here-document is data, read the way bash reads it: the lines
// after the newline of the line of the <<, up to the line that is exactly
// the delimiter (after the leading tabs, with <<-), with backslash-newline
// joined when the delimiter is unquoted. An unquoted body still expands
// $(…) and `…`, which are analysed as substitutions. A body that a shell or
// an interpreter reads (bash <<EOF, cat <<EOF | sh, ssh host <<EOF) is
// commands: a shell's is analysed as a command line, an interpreter's is
// unanalysable, both at least ask. What the lexer cannot read as bash does
// (a delimiter with an expansion, a line that spans lines before the body)
// falls back to the body read as commands, at least ask.

// hereDoc is one here-document of a line.
type hereDoc struct {
	pos    int // offset of the << in the line
	strip  bool
	delim  string
	quoted bool // some part of the delimiter was quoted: no expansion
	body   string
	read   bool // the body was read as data
}

// hereDoc records the << at pos, whose delimiter starts at lx.i.
func (lx *lexer) hereDoc(pos int, strip bool) {
	delim, quoted, ok := heredocDelim(lx.s[lx.i:])
	if !ok || arithmeticBefore(lx.s[:pos]) || slices.Contains(lx.parens, true) {
		// In (( )) and $[ ] bash reads << as a shift, and in an array or
		// an extended pattern as a syntax error it recovers from on the
		// next line: the lines after it stay commands.
		lx.opaque = append(lx.opaque, "here-document")
		lx.op("<<")
		return
	}
	hd := &hereDoc{pos: pos, strip: strip, delim: delim, quoted: quoted}
	lx.pending = append(lx.pending, hd)
	lx.op("<<")
	lx.toks[len(lx.toks)-1].doc = hd
}

// arithmeticBefore reports an arithmetic command or expansion ((( or $[)
// opened before a <<, where it may be a shift; $(( is read apart.
func arithmeticBefore(s string) bool {
	if strings.Contains(s, "$[") {
		return true
	}
	for i := strings.Index(s, "(("); i >= 0; {
		if i == 0 || s[i-1] != '$' {
			return true
		}
		j := strings.Index(s[i+2:], "((")
		if j < 0 {
			break
		}
		i += 2 + j
	}
	return false
}

// heredocDelim reads the delimiter word at the start of s as bash does:
// quote removal only, no expansion. ok is false when it holds something
// bash would treat apart ($(…), ${…}, `…`, $'…', $"…") or is empty.
func heredocDelim(s string) (delim string, quoted, ok bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		switch c {
		case ' ', '\t', '\n', ';', '&', '|', '<', '>', '(', ')':
			return b.String(), quoted, b.Len() > 0 || quoted
		case '\\':
			if i+1 >= len(s) {
				return "", false, false
			}
			if s[i+1] != '\n' {
				b.WriteByte(s[i+1])
				quoted = true
			}
			i += 2
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", false, false
			}
			b.WriteString(s[i+1 : i+1+end])
			quoted = true
			i += end + 2
		case '"':
			quoted = true
			i++
			closed := false
			for i < len(s) && !closed {
				switch d := s[i]; d {
				case '"':
					closed = true
					i++
				case '\\':
					if i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
						if s[i+1] != '\n' {
							b.WriteByte(s[i+1])
						}
						i += 2
						continue
					}
					b.WriteByte(d)
					i++
				case '$', '`':
					return "", false, false
				default:
					b.WriteByte(d)
					i++
				}
			}
			if !closed {
				return "", false, false
			}
		case '`':
			return "", false, false
		case '$':
			if i+1 < len(s) && strings.IndexByte("({'\"", s[i+1]) >= 0 {
				return "", false, false
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), quoted, b.Len() > 0 || quoted
}

// readBodies reads the bodies of the pending here-documents, from lx.i, the
// start of the line after the one of their <<.
func (lx *lexer) readBodies() {
	pending := lx.pending
	lx.pending = nil
	if len(pending) == 0 {
		return
	}
	if strings.Contains(lx.s[pending[0].pos:lx.i-1], "\n") {
		// Quotes, a substitution or a continuation spanned lines between
		// the << and the newline: where bash starts the body is not sure,
		// so the lines are read as commands.
		lx.opaque = append(lx.opaque, "here-document")
		return
	}
	for _, hd := range pending {
		hd.body, lx.i = heredocBody(lx.s, lx.i, hd)
		hd.read = true
	}
}

// heredocBody reads a body from start; next is where the line after the
// delimiter starts. As in bash, a line is compared after joining
// backslash-newline (unquoted delimiter) and removing the leading tabs
// (<<-); a body without its delimiter runs to the end. A line that starts
// with the delimiter and holds a ")" also ends it, as bash does inside
// $(…): ending early only leaves more to analyse as commands.
func heredocBody(s string, start int, hd *hereDoc) (body string, next int) {
	var out strings.Builder
	i := start
	for i < len(s) {
		lineStart := i
		var line strings.Builder
		for i < len(s) && s[i] != '\n' {
			if !hd.quoted && s[i] == '\\' && i+1 < len(s) {
				if s[i+1] == '\n' {
					i += 2 // the unquoted \<newline> pair disappears
					continue
				}
				line.WriteByte(s[i])
				line.WriteByte(s[i+1])
				i += 2
				continue
			}
			line.WriteByte(s[i])
			i++
		}
		end := min(i+1, len(s))
		text := line.String()
		if hd.strip {
			text = strings.TrimLeft(text, "\t")
		}
		if text == hd.delim {
			return out.String(), end
		}
		raw := s[lineStart:min(i, len(s))]
		if hd.strip {
			raw = strings.TrimLeft(raw, "\t")
		}
		if rest, ok := strings.CutPrefix(raw, hd.delim); ok && strings.Contains(rest, ")") {
			return out.String(), i - len(rest)
		}
		out.WriteString(text)
		out.WriteByte('\n')
		i = end
	}
	return out.String(), len(s)
}

// bodyExpansions finds what an unquoted body expands into commands: the
// texts of $(…) and `…`, also inside ${…} and $((…)), and why it is
// opaque. Quotes are not special in a body; variables are data.
func bodyExpansions(body string) (subs, why []string) {
	lx := &lexer{s: body}
	for lx.i < len(lx.s) {
		c := lx.s[lx.i]
		switch {
		case c == '\\':
			lx.i += 2
		case c == '`':
			lx.backtick()
		case c == '$' && lx.peek(1) == '(' && lx.peek(2) == '(':
			lx.i += 2
			inner, _ := lx.balanced('(', ')')
			if lx.i < len(lx.s) && lx.s[lx.i] == ')' {
				lx.i++
			}
			lx.opaque = append(lx.opaque, "expansão aritmética")
			s, w := bodyExpansions(inner)
			subs, why = append(subs, s...), append(why, w...)
		case c == '$' && lx.peek(1) == '(':
			lx.i++
			inner, ok := lx.balanced('(', ')')
			lx.sub(inner, ok, "substituição de comando $(…)")
		case c == '$' && lx.peek(1) == '{':
			lx.i++
			inner, _ := lx.balanced('{', '}')
			s, w := bodyExpansions(inner)
			subs, why = append(subs, s...), append(why, w...)
		default:
			lx.i++
		}
	}
	for _, t := range lx.toks {
		if t.op == "sub" {
			subs = append(subs, t.w.text)
		}
	}
	return subs, append(why, lx.opaque...)
}

// stdinPaths name the standard input as a file.
var stdinPaths = map[string]bool{"-": true, "/dev/stdin": true, "/dev/fd/0": true, "/proc/self/fd/0": true}

var (
	// stdinCode are the programs that run their standard input as a
	// program the motor does not read.
	stdinCode = map[string]bool{
		"python": true, "python2": true, "python3": true, "py": true, "pypy": true, "node": true, "nodejs": true,
		"deno": true, "bun": true, "ruby": true, "irb": true, "perl": true, "php": true, "lua": true, "rscript": true,
		"r": true, "julia": true, "osascript": true, "tsx": true, "ts-node": true, "groovy": true, "scala": true,
		"jshell": true, "ghci": true, "erl": true, "elixir": true, "tclsh": true, "wish": true, "expect": true,
		"gdb": true, "lldb": true, "sqlite3": true, "psql": true, "mysql": true, "mariadb": true, "crontab": true,
		"source": true, ".": true, "xargs": true, "parallel": true, "wscript": true, "cscript": true,
	}
	// stdinFileCode read a program from a file, which may be the input
	// (awk -f -, sed -f /dev/stdin, make -f -).
	stdinFileCode = map[string]bool{"awk": true, "gawk": true, "mawk": true, "nawk": true, "sed": true, "gsed": true, "make": true, "gmake": true, "bmake": true}
	// stdinRunners hand their input to a program elsewhere (another
	// machine, a container, another root): a shell named in the line reads
	// it as commands, anything else is unanalysable.
	stdinRunners = map[string]bool{
		"ssh": true, "docker": true, "podman": true, "nerdctl": true, "kubectl": true, "oc": true, "lxc": true,
		"incus": true, "chroot": true, "nsenter": true, "unshare": true, "systemd-run": true, "flatpak-spawn": true,
		"distrobox": true, "toolbox": true, "vagrant": true, "wsl": true, "machinectl": true,
	}
)

// stdinUse is how a program reads its standard input.
type stdinUse struct {
	name  string
	shell bool // as commands of a shell, in d
	d     dialect
	code  bool   // as a program the motor cannot read
	rep   string // the replace string of xargs -I, for a pipe into xargs
}

// stdinReader says how the simple command ws reads its standard input.
func (e *evaluator) stdinReader(ws []word, depth int) stdinUse {
	if len(ws) == 0 || depth > maxDepth {
		return stdinUse{}
	}
	if ws[0].expand {
		// A program only the shell resolves may be a shell.
		return stdinUse{name: ws[0].text, shell: true}
	}
	cn := canonical(ws[0].text)
	args := ws[1:]
	if stdinCode[cn] {
		return stdinUse{name: cn, code: true}
	}
	if inner, _, ok := e.unwrap(cn, args, &result{}); ok {
		return e.stdinReader(inner, depth+1)
	}
	base := normalizeInterpreter(cn)
	switch {
	case shells[base] || cn == "at" || cn == "batch":
		return stdinUse{name: cn, shell: true}
	case pwshNames[base] || cn == "cmd":
		return stdinUse{name: cn, shell: true, d: dialectPwsh}
	case stdinCode[base]:
		return stdinUse{name: cn, code: true}
	case stdinFileCode[cn] && readsProgramFile(wordsText(args)):
		return stdinUse{name: cn, code: true}
	case stdinRunners[cn]:
		for _, a := range args {
			an := normalizeInterpreter(canonical(a.text))
			switch {
			case shells[an]:
				return stdinUse{name: cn + " " + an, shell: true}
			case pwshNames[an]:
				return stdinUse{name: cn + " " + an, shell: true, d: dialectPwsh}
			}
		}
		if cn == "ssh" || cn == "wsl" {
			// Without a shell named, the remote or WSL login shell may be
			// the one that reads it.
			return stdinUse{name: cn, shell: true}
		}
		return stdinUse{name: cn, code: true}
	}
	return stdinUse{}
}

// readsProgramFile reports an option that reads the program from the
// standard input (-f -, -f/dev/stdin, --file=-).
func readsProgramFile(args []string) bool {
	for i, a := range args {
		switch {
		case (a == "-f" || a == "--file" || a == "--makefile") && i+1 < len(args) && stdinPaths[args[i+1]]:
			return true
		case strings.HasPrefix(a, "-f") && !strings.HasPrefix(a, "--") && stdinPaths[a[2:]]:
			return true
		case strings.HasPrefix(a, "--"):
			if _, v, ok := strings.Cut(a, "="); ok && stdinPaths[v] {
				return true
			}
		}
	}
	return false
}

// stdinReaders are the parts that read the input of part i: itself and the
// parts after it in its pipeline. A grouping is followed through, which
// may take in more parts than the pipeline: that only analyses more. A
// part without a command redirects a compound command ({ bash; } <<EOF):
// any part of the line may read it.
func stdinReaders(parts []simple, i int) []int {
	if len(parts[i].words) == 0 {
		out := make([]int, len(parts))
		for j := range parts {
			out[j] = j
		}
		return out
	}
	out := []int{i}
	for j := i; j+1 < len(parts); j++ {
		switch parts[j].next {
		case "|", "|&", "(", ")", "{", "}":
			out = append(out, j+1)
		default:
			return out
		}
	}
	return out
}

// stdinBodies analyses the here-documents and here-strings of part i for
// the programs that read them.
func (e *evaluator) stdinBodies(parts []simple, i int, st *shellState, depth int) result {
	res := newResult()
	if len(parts[i].stdin) == 0 {
		return res
	}
	for _, j := range stdinReaders(parts, i) {
		u := e.stdinReader(parts[j].words, 0)
		why := fmt.Sprintf("here-document lido por %s", u.name)
		switch {
		case u.shell:
			for _, b := range parts[i].stdin {
				if depth+1 > maxDepth {
					res.fixedDeny("comando aninhado demais para ser analisado")
					continue
				}
				res.merge(e.shellIn(b, st.cwds[0], u.d, depth+1))
			}
			res.atLeastAsk(why)
		case u.code:
			res.atLeastAsk(why)
		}
	}
	return res
}

// stdinRisk is stdinBodies for the risk: a body a shell reads is classified
// as a command line, and both kinds of reader are at least medio.
func (w *riskWalker) stdinRisk(parts []simple, i int, st *shellState, depth int) riskOut {
	var out riskOut
	if len(parts[i].stdin) == 0 {
		return out
	}
	for _, j := range stdinReaders(parts, i) {
		u := w.e.stdinReader(parts[j].words, 0)
		if !u.shell && !u.code {
			continue
		}
		if u.shell {
			for _, b := range parts[i].stdin {
				out.raise(w.line(b, st.cwds[0], u.d, depth+1))
			}
		}
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: here-document lido por " + u.name})
	}
	return out
}
