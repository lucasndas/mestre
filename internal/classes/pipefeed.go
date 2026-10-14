package classes

import (
	"fmt"
	"strings"
)

// Pipelines into a reader of commands
// (docs/classes/classificacao-de-risco.md#comandos-de-shell). A shell or
// an interpreter that reads its commands or code from the pipe (echo … |
// bash, … | pwsh -Command -, … | xargs sh -c, … | python -c "…input()…",
// curl … | sh) runs text the line does not show: the part is alto, like
// eval. When the producer is a literal the motor reads (echo, printf, a
// PowerShell string), that literal is analysed as the command line it is,
// so a fixed rule in it denies, and the part is at least ask (medio).

// pipeReader says whether the simple command ws, fed by a pipe, runs what
// it reads as commands or code. xargs is apart: it appends what it reads
// to the command it runs.
func (e *evaluator) pipeReader(ws []word, depth int) (u stdinUse, xargs []word) {
	if len(ws) == 0 || depth > maxDepth {
		return stdinUse{}, nil
	}
	if ws[0].expand {
		return stdinUse{name: ws[0].text, shell: true}, nil
	}
	cn := canonical(ws[0].text)
	args := ws[1:]
	if cn == "xargs" {
		if inner, _, ok := e.unwrap(cn, args, &result{}); ok && len(inner) > 0 {
			if u, _ := e.pipeReader(inner, depth+1); u.shell || u.code {
				// xargs sh -c: what it reads becomes the shell's code.
				u.name = "xargs " + u.name
				return u, nil
			}
			if rep := xargsReplace(args); rep != "" {
				icn := normalizeInterpreter(canonical(inner[0].text))
				if nested, nd, _, ok := nestedShell(icn, inner[1:]); ok && strings.Contains(nested, rep) {
					// xargs -I{} sh -c '{}': the input is put into the code.
					return stdinUse{name: "xargs " + icn, shell: true, d: nd}, nil
				}
			}
			return stdinUse{name: "xargs", rep: xargsReplace(args)}, inner
		}
		return stdinUse{}, nil // xargs alone echoes what it reads
	}
	if inner, _, ok := e.unwrap(cn, args, &result{}); ok {
		return e.pipeReader(inner, depth+1)
	}
	base := normalizeInterpreter(cn)
	switch {
	case shells[base], pwshNames[base], cn == "cmd", cn == "source", cn == ".":
		if readsScriptFromStdin(cn, args) {
			d := dialectPOSIX
			if pwshNames[base] || cn == "cmd" {
				d = dialectPwsh
			}
			return stdinUse{name: cn, shell: true, d: d}, nil
		}
		return stdinUse{}, nil
	case stdinCode[base] || stdinCode[cn]:
		if s, ok := interpreterScript(base, args); ok && !stdinPaths[s.text] && !inlineCode(base, args) {
			return stdinUse{}, nil // a script file: the pipe is its data
		}
		return stdinUse{name: cn, code: true}, nil
	case stdinFileCode[cn] && readsProgramFile(wordsText(args)):
		return stdinUse{name: cn, code: true}, nil
	case stdinRunners[cn]:
		return e.stdinReader(ws, depth), nil
	}
	return stdinUse{}, nil
}

// readsScriptFromStdin reports a shell that takes its script from the
// standard input: no script and no command text (bash, sh -s, pwsh,
// pwsh -Command -, cmd without /c), or a script named - or /dev/stdin
// (bash -, pwsh -File -, source /dev/stdin).
func readsScriptFromStdin(cn string, args []word) bool {
	base := normalizeInterpreter(cn)
	if cn == "source" || cn == "." {
		return len(args) > 0 && stdinPaths[args[0].text]
	}
	if s, ok := interpreterScript(base, args); ok && stdinPaths[s.text] {
		return true
	}
	if nested, _, _, ok := nestedShell(base, args); ok && nested == "" {
		return true
	}
	return false
}

// stdinScriptShell reports a shell or source whose script operand is the
// standard input (bash -, sh /dev/stdin, pwsh -File -, . /dev/stdin).
func stdinScriptShell(cn string, args []word) bool {
	base := normalizeInterpreter(cn)
	if cn == "source" || cn == "." {
		return len(args) > 0 && stdinPaths[args[0].text]
	}
	if !shells[base] && !pwshNames[base] {
		return false
	}
	s, ok := interpreterScript(base, args)
	return ok && stdinPaths[s.text]
}

// inlineCode reports an interpreter given its code in the line (python -c,
// node -e, perl -ne, ruby -pe, php -r): fed by a pipe, the code may run
// what it reads.
func inlineCode(base string, args []word) bool {
	for _, a := range args {
		t := a.text
		if t == "--" || !strings.HasPrefix(t, "-") {
			return false
		}
		name, _, _ := strings.Cut(t, "=")
		if interpreterCodeOpts[name] || name == "-p" || name == "--print" || name == "-r" || name == "-R" {
			return true
		}
		if (base == "perl" || base == "ruby") && !strings.HasPrefix(t, "--") && strings.ContainsAny(t[1:], "eE") {
			return true
		}
	}
	return false
}

// literalOutput is what the producer p of a pipe writes, when the motor
// reads it: echo or printf of literal words (POSIX), echo, Write-Output or
// a bare string (PowerShell). Escapes, options and directives it does not
// spell out make it unreadable.
func literalOutput(p *simple, d dialect) (string, bool) {
	if len(p.words) == 0 || len(p.redirs) > 0 || len(p.subs) > 0 || len(p.opaque) > 0 || len(p.assigns) > 0 {
		return "", false
	}
	for _, w := range p.words {
		if w.expand || w.glob || w.tilde {
			return "", false
		}
	}
	texts := wordsText(p.words)
	cn := canonical(texts[0])
	args := texts[1:]
	if d == dialectPwsh {
		switch {
		case cn == "echo" || cn == "write-output" || cn == "write":
			return strings.Join(args, "\n"), true
		case len(p.words) == 1 && p.words[0].quoted:
			return texts[0], true // a string expression writes itself
		}
		return "", false
	}
	switch cn {
	case "echo":
		for len(args) > 0 && (args[0] == "-n" || args[0] == "-E") {
			args = args[1:]
		}
		for _, a := range args {
			if strings.Contains(a, `\`) || strings.HasPrefix(a, "-") {
				return "", false // echo -e, and the escapes dash's echo reads
			}
		}
		return strings.Join(args, " "), true
	case "printf":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return "", false
		}
		return printfLiteral(args[0], args[1:])
	}
	return "", false
}

// printfLiteral spells printf with only %s, %% and the escapes \n, \t, \\,
// \" and \': the format is reused while arguments remain, as printf does.
func printfLiteral(format string, args []string) (string, bool) {
	var out strings.Builder
	for first := true; first || len(args) > 0; first = false {
		used := false
		for i := 0; i < len(format); i++ {
			c := format[i]
			switch {
			case c == '\\' && i+1 < len(format):
				i++
				switch format[i] {
				case 'n':
					out.WriteByte('\n')
				case 't':
					out.WriteByte('\t')
				case '\\', '"', '\'':
					out.WriteByte(format[i])
				default:
					return "", false
				}
			case c == '\\':
				return "", false
			case c == '%' && i+1 < len(format) && format[i+1] == '%':
				out.WriteByte('%')
				i++
			case c == '%' && i+1 < len(format) && format[i+1] == 's':
				if len(args) > 0 {
					out.WriteString(args[0])
					args = args[1:]
				}
				used = true
				i++
			case c == '%':
				return "", false
			default:
				out.WriteByte(c)
			}
		}
		if !used {
			break // arguments without a %s are not printed
		}
	}
	return out.String(), true
}

// pipeFed is the reader of the input of part i and the literal that feeds
// it, when part i reads commands or code from a pipe.
type pipeFed struct {
	u       stdinUse
	xargs   []word
	lit     string
	literal bool
}

func (e *evaluator) pipeFeed(parts []simple, i int, d dialect) (pipeFed, bool) {
	if i == 0 || (parts[i-1].next != "|" && parts[i-1].next != "|&") {
		return pipeFed{}, false
	}
	u, xargs := e.pipeReader(parts[i].words, 0)
	if !u.shell && !u.code && xargs == nil {
		return pipeFed{}, false
	}
	f := pipeFed{u: u, xargs: xargs}
	f.lit, f.literal = literalOutput(&parts[i-1], d)
	return f, true
}

// texts are the command lines the literal makes: itself for a shell or an
// interpreter, the command it completes for xargs. xargs removes quotes
// from what it reads (echo '"git push"' | xargs sh -c runs git push), so
// for it the literal is also read with its quotes removed.
func (f pipeFed) texts() []string {
	lits := []string{f.lit}
	if strings.HasPrefix(f.u.name, "xargs") || f.xargs != nil {
		lits = append(lits, xargsWords(f.lit))
	}
	if f.xargs != nil {
		cmd := strings.Join(wordsText(f.xargs), " ")
		for i, l := range lits {
			lits[i] = cmd + " " + l
		}
	}
	return lits
}

// xargsWords is the input of xargs with its quotes removed, as xargs splits
// it into words.
func xargsWords(lit string) string {
	lx := &lexer{s: lit}
	lx.run()
	var ws []string
	for _, t := range lx.toks {
		if t.op == "" {
			ws = append(ws, t.w.text)
		}
	}
	return strings.Join(ws, " ")
}

func (f pipeFed) dialect(d dialect) dialect {
	if f.u.shell {
		return f.u.d
	}
	return d
}

// pipeDecision is the decision for a pipe into a reader of commands: the
// literal is analysed as its command line, and the part is at least ask.
func (e *evaluator) pipeDecision(parts []simple, i int, d dialect, st *shellState, depth int) result {
	res := newResult()
	f, ok := e.pipeFeed(parts, i, d)
	if !ok {
		return res
	}
	if f.literal {
		if depth+1 > maxDepth {
			res.fixedDeny("comando aninhado demais para ser analisado")
			return res
		}
		for _, text := range f.texts() {
			res.merge(e.shellIn(text, st.cwds[0], f.dialect(d), depth+1))
		}
	}
	if f.u.shell || f.u.code {
		res.atLeastAsk("entrada do pipe lida por " + f.u.name)
	}
	return res
}

// pipeRisk is the risk of a pipe into a reader of commands: alto, unless
// the literal that feeds it is legible, which is then classified (at least
// medio). xargs only rises when its input names the program or the git
// subcommand.
func (w *riskWalker) pipeRisk(parts []simple, i int, d dialect, st *shellState, depth int) (riskOut, *string) {
	f, ok := w.e.pipeFeed(parts, i, d)
	if !ok {
		return riskOut{}, nil
	}
	if f.literal {
		var out riskOut
		for _, text := range f.texts() {
			if illegible(text, f.dialect(d)) {
				return riskOut{risk: RiskHigh, rule: "entrada do pipe de texto ilegível, lida por " + f.u.name}, nil
			}
			out.raise(w.line(text, st.cwds[0], f.dialect(d), depth+1))
		}
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: entrada do pipe lida por " + f.u.name})
		return out, &f.lit
	}
	if f.xargs != nil && !xargsChoosesProgram(f.xargs, f.u.rep) {
		return riskOut{}, nil // the command's own analysis holds
	}
	return riskOut{risk: RiskHigh, rule: fmt.Sprintf("comandos que o motor não lê, vindos do pipe e lidos por %s", f.u.name)}, nil
}

// xargsReplace is the replace string of xargs -I R, -IR, -i, --replace[=R]
// ("{}" by default); "" without one.
func xargsReplace(args []word) string {
	for i, a := range args {
		t := a.text
		switch {
		case t == "--":
			return ""
		case t == "-I":
			if i+1 < len(args) {
				return args[i+1].text
			}
			return ""
		case t == "-i" || t == "--replace":
			return "{}"
		case strings.HasPrefix(t, "-I") || strings.HasPrefix(t, "-i"):
			return t[2:]
		case strings.HasPrefix(t, "--replace="):
			return strings.TrimPrefix(t, "--replace=")
		case !strings.HasPrefix(t, "-"):
			return ""
		}
	}
	return ""
}

// xargsChoosesProgram reports the commands of xargs whose program or git
// subcommand comes from the input (xargs git, xargs -I{} {} …).
func xargsChoosesProgram(inner []word, rep string) bool {
	if rep != "" && strings.Contains(inner[0].text, rep) {
		return true
	}
	return canonical(inner[0].text) == "git" && gitSubText(inner[1:]) == ""
}
