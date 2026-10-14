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

// forcesCode reports a construct that executes what the motor cannot read,
// so the part is at least medio (ask); a literal command is classified, and
// decided, as the command line it is, and a write only applies the path
// rules.
func (p progCode) forcesCode() bool { return p.code }

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
			// The program is a file the motor does not read as a command
			// (-f, an -i include or an -l extension): it may use system(),
			// so it is inline code. gawk -i inplace writes its files.
			return progCode{code: true, execInput: true, writes: awkInplace(args), why: cn + " -f (programa que o motor não lê)"}, true
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
		case awkFileOpts[t]:
			// gawk -i lib (an include, or the inplace extension), -l ext, -E
			// file and mawk -W exec file: code the motor does not read.
			fromFile = true
			i += 2
		case awkFileOptPrefix(t):
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

// awkFiles are the files an awk command reads: the program files of -f and
// the operands after the program, without the assignments (var=value).
func awkFiles(args []word) []word {
	var out []word
	inline := true
	i := 0
	for ; i < len(args); i++ {
		t := args[i].text
		switch {
		case t == "--":
			i++
			goto operands
		case t == "-f" || t == "--file" || t == "-E" || t == "--exec":
			inline = false
			if i+1 < len(args) {
				out = append(out, args[i+1])
			}
			i++
		case t == "-e" || t == "--source":
			inline = false
			i++
		case t == "-F" || t == "-v" || t == "--field-separator" || t == "--assign" || awkFileOpts[t]:
			i++
		case strings.HasPrefix(t, "-") && t != "-":
		default:
			goto operands
		}
	}
operands:
	if inline && i < len(args) {
		i++ // the program text
	}
	for ; i < len(args); i++ {
		if args[i].assign < 0 && !strings.Contains(args[i].text, "=") || strings.ContainsAny(args[i].text, `/\`) {
			out = append(out, args[i])
		}
	}
	return out
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

// awkFileOpts are the options of awk that load code from elsewhere: an
// include (-i, which with inplace makes gawk write its files), an
// extension (-l), a program file (-E) and mawk's -W.
var awkFileOpts = map[string]bool{"-i": true, "--include": true, "-l": true, "--load": true, "-E": true, "--exec": true, "-W": true}

func awkFileOptPrefix(t string) bool {
	for _, p := range []string{"--include=", "--load=", "--exec=", "-i", "-l", "-E", "-W"} {
		if strings.HasPrefix(t, p) && t != p {
			return true
		}
	}
	return false
}

// awkInplace are the files gawk -i inplace writes: the operands after the
// program (or after -f/-e), the assignments apart.
func awkInplace(args []word) []word {
	inplace := false
	for i, a := range args {
		t := a.text
		if (t == "-i" || t == "--include") && i+1 < len(args) && args[i+1].text == "inplace" || t == "-iinplace" || t == "--include=inplace" {
			inplace = true
		}
	}
	if !inplace {
		return nil
	}
	var out []word
	for _, a := range args {
		if !strings.HasPrefix(a.text, "-") && a.text != "inplace" && !strings.Contains(a.text, "{") && !strings.Contains(a.text, "=") {
			out = append(out, a)
		}
	}
	return out
}

var (
	awkSystemLiteral  = regexp.MustCompile(`system\s*\(\s*"((?:\\.|[^"\\])*)"\s*\)`)
	awkSystemAny      = regexp.MustCompile(`\bsystem\s*\(`)
	awkPipeLiteral    = regexp.MustCompile(`\|&?\s*"((?:\\.|[^"\\])*)"`)
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
	// Each construct has a word or a character it cannot do without, so the
	// regular expressions run only over a program that holds it: the scans of
	// a long program that has none cost nothing (the bound of the analysis).
	var sysLits, pipeLits, getLits [][]string
	hasSystem, hasGetPipe, hasGetLit := false, false, false
	if strings.Contains(prog, "system") {
		hasSystem = awkSystemAny.MatchString(prog)
		if hasSystem {
			sysLits = awkSystemLiteral.FindAllStringSubmatch(prog, -1)
		}
	}
	if strings.Contains(prog, "|") {
		pipeLits = awkPipeLiteral.FindAllStringSubmatch(prog, -1)
	}
	if strings.Contains(prog, "getline") {
		getLits = awkGetlineLiteral.FindAllStringSubmatch(prog, -1)
		hasGetLit = len(getLits) > 0
		hasGetPipe = awkGetlinePipe.MatchString(prog)
	}
	hasLoad := strings.Contains(prog, "@") && awkLoad.MatchString(prog)
	hasPipe := len(pipeLits) > 0 || hasGetPipe || hasGetLit
	writes, dynamic := awkWrites(prog)
	if !hasSystem && !hasPipe && !hasLoad && len(writes) == 0 && dynamic == "" {
		return progCode{}, false
	}
	add(sysLits)
	add(pipeLits)
	add(getLits)
	p.writes = distinctWrites(writes)
	// A system() or getline command whose text is not a plain literal is
	// dynamic: the motor cannot read it.
	if hasSystem && len(sysLits) == 0 {
		p.code = true
	}
	if hasGetPipe || hasLoad || dynamic != "" {
		p.code = true
	}
	p.execInput = hasSystem || hasGetPipe || hasGetLit || dynamic != ""
	p.why = cn + " executa comando (system/pipe/getline)"
	if dynamic != "" {
		p.why = cn + " " + dynamic
	}
	return p, true
}

// distinctWrites drops the repeated targets: the path rules decide the same
// for the same text, so a program that writes one file ten thousand times
// costs one check, not ten thousand (the bound of the analysis).
func distinctWrites(ws []word) []word {
	if len(ws) < 2 {
		return ws
	}
	seen := make(map[string]bool, len(ws))
	out := ws[:0]
	for _, w := range ws {
		if !seen[w.text] {
			seen[w.text] = true
			out = append(out, w)
		}
	}
	return out
}

// awkWrites reads an awk program with its strings and regular expressions
// blanked: the literal files its print and printf write (> "f", >> "f"),
// and what it does that the motor cannot read: a pipe or a write to a
// computed target (print | cmd, print > $1), a coprocess, an indirect call
// (@f(), which can call system).
func awkWrites(prog string) (writes []word, dynamic string) {
	code := awkBlank(prog)
	stmt, depth := 0, 0
	for i := 0; i < len(code); i++ {
		switch c := code[i]; c {
		case ';', '{', '}', '\n':
			stmt, depth = i+1, 0
		case '(':
			depth++
		case ')':
			depth--
		case '@':
			j := i + 1
			for j < len(code) && (isNameByte(code[j]) || code[j] == ':') {
				j++
			}
			name := code[i+1 : j]
			for j < len(code) && code[j] == ' ' {
				j++
			}
			if name != "" && name != "load" && name != "include" && name != "namespace" && j < len(code) && code[j] == '(' {
				dynamic = "chamada indireta @" + name + "()"
			}
		case '|':
			if i+1 < len(code) && code[i+1] == '|' || i > 0 && code[i-1] == '|' {
				continue
			}
			j := i + 1
			if j < len(code) && code[j] == '&' {
				j++
			}
			for j < len(code) && (code[j] == ' ' || code[j] == '\t') {
				j++
			}
			if j < len(code) && code[j] != '"' && !strings.HasPrefix(code[j:], "getline") {
				dynamic = "pipe para comando calculado"
			}
		case '>':
			st := strings.TrimSpace(code[stmt:i])
			if depth > 0 || !strings.HasPrefix(st, "print") || i+1 < len(code) && code[i+1] == '=' {
				continue // a comparison
			}
			j := i + 1
			if j < len(code) && code[j] == '>' {
				j++
			}
			for j < len(code) && (code[j] == ' ' || code[j] == '\t') {
				j++
			}
			if j < len(code) && code[j] == '"' {
				if m := awkStringAt(prog, j); m != "" {
					if lit, ok := awkUnescape(m); ok && lit != "" && !isDevice(lit) {
						writes = append(writes, word{text: lit, assign: -1})
						i = j
						continue
					}
					if lit, _ := awkUnescape(m); isDevice(lit) {
						i = j
						continue
					}
				}
			}
			dynamic = "escrita em arquivo calculado"
			i = j
		}
	}
	return writes, dynamic
}

// awkBlank is the program with the text of its strings and of its regular
// expressions replaced by spaces, so the operators inside them do not count.
func awkBlank(prog string) string {
	b := []byte(prog)
	last := byte(0) // the last character that is not a space
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			for i++; i < len(b) && b[i] != '"'; i++ {
				if b[i] == '\\' && i+1 < len(b) {
					b[i] = ' '
					i++
				}
				b[i] = ' '
			}
			last = '"'
		case c == '/' && (last == 0 || strings.IndexByte("(,{};!~&|\n=", last) >= 0):
			for i++; i < len(b) && b[i] != '/' && b[i] != '\n'; i++ {
				if b[i] == '\\' && i+1 < len(b) {
					b[i] = ' '
					i++
				}
				b[i] = ' '
			}
			last = '/'
		case c == '#':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c != ' ' && c != '\t':
			last = c
		}
	}
	return string(b)
}

// awkStringAt is the text of the string literal that opens at prog[i].
func awkStringAt(prog string, i int) string {
	for j := i + 1; j < len(prog); j++ {
		switch prog[j] {
		case '\\':
			j++
		case '"':
			return prog[i+1 : j]
		}
	}
	return ""
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
	p.writes = distinctWrites(p.writes)
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
