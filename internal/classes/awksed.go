package classes

import (
	"regexp"
	"strings"
)

// awk and sed run inline programs that can execute commands
// (docs/classes/classificacao-de-risco.md#sinais-de-risco). An awk program
// with system(), a command pipe (print | "cmd", "cmd" | getline) or
// @load/@include, and a sed program with the e command, the e flag of
// s///, or a w/W write, are inline code: at least medio (the
// interpretador_com_codigo signal), never baixo. When the command string
// inside is a literal the motor reads (awk 'BEGIN{system("git push")}',
// sed '1e git push'), it is analysed as a command line too, so a fixed
// rule in it denies. A sed w/W/s///w writes its target, so the path rules
// apply (a protected target is a fixed deny). Ordinary awk and sed
// (awk '{print $1}', sed -n '1,5p', sed -i 's/a/b/' f) stay baixo.

var (
	awkNames = map[string]bool{"awk": true, "gawk": true, "mawk": true, "nawk": true, "goawk": true, "frawk": true}
	sedNames = map[string]bool{"sed": true, "gsed": true, "ssed": true, "minised": true}
)

// progCode describes the dangerous parts of an awk or sed program.
type progCode struct {
	cmds      []string // literal command lines to analyse as a command line
	writes    []word   // file targets a sed w/W/s///w writes
	code      bool     // dynamic execution the motor cannot read: force at least medio/ask
	execInput bool     // may run what it reads from stdin: a pipe code reader
	why       string
}

func (p progCode) ok() bool { return p.code || len(p.cmds) > 0 || len(p.writes) > 0 }

// forcesCode reports a construct that executes a command, so the part is at
// least medio (a bare write only applies the path rules).
func (p progCode) forcesCode() bool { return p.code || len(p.cmds) > 0 }

// progCodeOf analyses the program an awk or sed command runs. ok is false
// for any other program, and for an awk or sed program that shows none of
// the dangerous constructs.
func (e *evaluator) progCodeOf(cn string, args []word) (progCode, bool) {
	switch {
	case awkNames[cn]:
		prog, fromFile, ok := awkProgram(args)
		if !ok {
			return progCode{}, false
		}
		if fromFile {
			// The program is a file the motor does not read as a command:
			// it may use system(), so it is inline code.
			return progCode{code: true, execInput: true, why: cn + " -f (programa que o motor não lê)"}, true
		}
		return scanAwk(cn, prog)
	case sedNames[cn]:
		prog, fromFile, ok := sedProgram(args)
		if fromFile {
			return progCode{code: true, execInput: true, why: cn + " -f (programa que o motor não lê)"}, true
		}
		if !ok {
			return progCode{}, false
		}
		return scanSed(cn, prog)
	}
	return progCode{}, false
}

// awkProgram is the inline program of an awk command, and whether it comes
// from a -f file (which the motor does not read here). ok is false when no
// inline program is given, or it is a word only the shell resolves.
func awkProgram(args []word) (prog string, fromFile, ok bool) {
	var progs []string
	expectValue := map[string]bool{"-F": true, "-v": true, "--field-separator": true, "--assign": true}
	i := 0
	for i < len(args) {
		a := args[i]
		t := a.text
		switch {
		case t == "--":
			i++
			goto operand
		case t == "-f" || t == "--file":
			fromFile = true
			i += 2
		case strings.HasPrefix(t, "--file="):
			fromFile = true
			i++
		case t == "-e" || t == "--source":
			if i+1 < len(args) {
				w := args[i+1]
				if w.expand {
					return "", fromFile, false
				}
				progs = append(progs, w.text)
			}
			i += 2
		case strings.HasPrefix(t, "--source="):
			progs = append(progs, strings.TrimPrefix(t, "--source="))
			i++
		case strings.HasPrefix(t, "-f"):
			fromFile = true
			i++
		case expectValue[t]:
			i += 2
		case strings.HasPrefix(t, "-") && t != "-":
			i++
		default:
			goto operand
		}
	}
operand:
	if len(progs) == 0 && i < len(args) {
		if args[i].expand {
			return "", fromFile, false
		}
		progs = append(progs, args[i].text)
	}
	if len(progs) == 0 {
		return "", fromFile, fromFile // -f without inline text still counts
	}
	return strings.Join(progs, "\n"), fromFile, true
}

// sedProgram joins the inline and -e scripts of a sed command; fromFile is
// true when a -f program file is given (not read here).
func sedProgram(args []word) (prog string, fromFile, ok bool) {
	var scripts []string
	haveScript := false
	valueOpt := map[string]bool{"-l": true, "--line-length": true}
	i := 0
	for i < len(args) {
		a := args[i]
		t := a.text
		switch {
		case t == "--":
			i++
			goto operand
		case t == "-e" || t == "--expression":
			haveScript = true
			if i+1 < len(args) {
				if args[i+1].expand {
					return "", fromFile, false
				}
				scripts = append(scripts, args[i+1].text)
			}
			i += 2
		case strings.HasPrefix(t, "--expression="):
			haveScript = true
			scripts = append(scripts, strings.TrimPrefix(t, "--expression="))
			i++
		case strings.HasPrefix(t, "-e"):
			haveScript = true
			if !a.expand {
				scripts = append(scripts, t[2:])
			}
			i++
		case t == "-f" || t == "--file":
			fromFile, haveScript = true, true
			i += 2
		case strings.HasPrefix(t, "--file="), strings.HasPrefix(t, "-f"):
			fromFile, haveScript = true, true
			i++
		case valueOpt[t]:
			i += 2
		case strings.HasPrefix(t, "-") && t != "-":
			i++
		default:
			goto operand
		}
	}
operand:
	if !haveScript && i < len(args) {
		if args[i].expand {
			return "", fromFile, false
		}
		scripts = append(scripts, args[i].text)
		haveScript = true
	}
	return strings.Join(scripts, "\n"), fromFile, haveScript
}

var (
	awkSystemLiteral  = regexp.MustCompile(`system\s*\(\s*"((?:\\.|[^"\\])*)"\s*\)`)
	awkSystemAny      = regexp.MustCompile(`\bsystem\s*\(`)
	awkPipeLiteral    = regexp.MustCompile(`\|\s*"((?:\\.|[^"\\])*)"`)
	awkGetlineLiteral = regexp.MustCompile(`"((?:\\.|[^"\\])*)"\s*\|\s*&?\s*getline`)
	awkGetlinePipe    = regexp.MustCompile(`\|\s*&?\s*getline|getline\s*<\s*"`)
	awkLoad           = regexp.MustCompile(`@(load|include)\b`)
)

// scanAwk finds the dangerous constructs of an awk program.
func scanAwk(cn, prog string) (progCode, bool) {
	var p progCode
	add := func(groups [][]string) {
		for _, m := range groups {
			if lit, ok := awkUnescape(m[1]); ok {
				p.cmds = append(p.cmds, lit)
			} else {
				p.code = true // an escape the motor does not read
			}
		}
	}
	hasSystem := awkSystemAny.MatchString(prog)
	hasPipe := awkPipeLiteral.MatchString(prog) || awkGetlinePipe.MatchString(prog) || awkGetlineLiteral.MatchString(prog)
	hasLoad := awkLoad.MatchString(prog)
	if !hasSystem && !hasPipe && !hasLoad {
		return progCode{}, false
	}
	add(awkSystemLiteral.FindAllStringSubmatch(prog, -1))
	add(awkPipeLiteral.FindAllStringSubmatch(prog, -1))
	add(awkGetlineLiteral.FindAllStringSubmatch(prog, -1))
	// A system() or getline command whose text is not a plain literal is
	// dynamic: the motor cannot read it.
	if hasSystem && len(awkSystemLiteral.FindAllString(prog, -1)) == 0 {
		p.code = true
	}
	if awkGetlinePipe.MatchString(prog) {
		p.code = true
	}
	if hasLoad {
		p.code = true
	}
	p.execInput = hasSystem || awkGetlinePipe.MatchString(prog) || awkGetlineLiteral.MatchString(prog)
	p.why = cn + " executa comando (system/pipe/getline)"
	return p, true
}

// awkUnescape turns an awk string literal into its text, for the common
// escapes; ok is false for an escape the motor does not read, so the
// command is treated as unreadable.
func awkUnescape(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			return "", false
		}
		switch s[i] {
		case '"', '\\', '/':
			b.WriteByte(s[i])
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		default:
			return "", false
		}
	}
	return b.String(), true
}

// scanSed walks a sed program for the e command, the e/w flags of s/// and
// the w/W writes.
func scanSed(cn, prog string) (progCode, bool) {
	var p progCode
	i := 0
	skipAddresses := func() {
		for i < len(prog) {
			c := prog[i]
			switch {
			case c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '!' || c == '{' || c == '}':
				i++
			case c >= '0' && c <= '9' || c == '$' || c == ',' || c == '~' || c == '+':
				i++
			case c == '/':
				i = skipDelimited(prog, i, '/')
			case c == '\\' && i+1 < len(prog):
				d := prog[i+1]
				i = skipDelimited(prog, i+2, d)
			default:
				return
			}
		}
	}
	readToEOL := func() string {
		start := i
		for i < len(prog) && prog[i] != '\n' {
			i++
		}
		return strings.TrimSpace(prog[start:i])
	}
	for i < len(prog) {
		skipAddresses()
		if i >= len(prog) {
			break
		}
		cmd := prog[i]
		i++
		switch cmd {
		case 's', 'y':
			if i >= len(prog) {
				break
			}
			delim := prog[i]
			i++                           // past the delimiter
			i = skipField(prog, i, delim) // regex or source
			i = skipField(prog, i, delim) // replacement or dest
			if cmd == 's' {
				sedSFlags(prog, &i, &p)
			}
		case 'e':
			if c := readToEOL(); c != "" {
				p.cmds = append(p.cmds, c)
			} else {
				// Executes the pattern space, which may be the input.
				p.code, p.execInput = true, true
			}
		case 'w', 'W':
			if f := readToEOL(); f != "" {
				p.writes = append(p.writes, word{text: f, assign: -1})
			}
		case 'r', 'R':
			readToEOL() // reads a file: not an execution
		case 'a', 'i', 'c':
			readToEOL() // appended text
		default:
			for i < len(prog) && prog[i] != '\n' && prog[i] != ';' {
				i++
			}
		}
	}
	if !p.ok() {
		return progCode{}, false
	}
	p.why = cn + " executa ou escreve (e/w)"
	return p, true
}

// sedSFlags reads the flags after an s/// and records an e flag (execute)
// or a w file (write).
func sedSFlags(prog string, i *int, p *progCode) {
	for *i < len(prog) {
		c := prog[*i]
		switch {
		case c == 'e':
			p.code = true
			*i++
		case c == 'w':
			*i++
			start := *i
			for *i < len(prog) && prog[*i] != '\n' {
				*i++
			}
			if f := strings.TrimSpace(prog[start:*i]); f != "" {
				p.writes = append(p.writes, word{text: f, assign: -1})
			}
			return
		case c >= '0' && c <= '9' || c == 'g' || c == 'i' || c == 'I' || c == 'm' || c == 'M' || c == 'p':
			*i++
		default:
			return
		}
	}
}

// skipDelimited skips /…/ from the opening delimiter at i (at prog[i]==d),
// honouring backslash escapes; it returns the index after the close.
func skipDelimited(prog string, i int, d byte) int {
	i++ // past the open
	for i < len(prog) {
		if prog[i] == '\\' {
			i += 2
			continue
		}
		if prog[i] == d {
			return i + 1
		}
		i++
	}
	return i
}

// skipField skips one field of an s/// or y/// up to and past the next
// unescaped delimiter; i is just after the previous delimiter.
func skipField(prog string, i int, delim byte) int {
	for i < len(prog) {
		if prog[i] == '\\' {
			i += 2
			continue
		}
		if prog[i] == delim {
			return i + 1
		}
		i++
	}
	return i
}
