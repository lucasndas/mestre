package classes

import (
	"regexp"
	"strings"
)

// The command line of a shell tool is split into simple commands
// (docs/classes/permissoes.md#regras-fixas-de-segurança and the commands
// rule of docs/classes/formato-das-permissoes.md): at &&, ||, ;, |, &, line
// breaks and the grouping parentheses and braces. Quotes and escapes are
// removed from the words. What the dispatcher cannot evaluate (command
// substitution, variables, loops, eval and nested shells) is recorded so
// the part ends at least in ask; the text of a substitution is still
// analysed on its own, so a fixed rule inside it denies. The body of a
// here-document is data (heredoc.go), unless a shell or an interpreter
// reads it.

// dialect is the shell grammar: POSIX sh/bash, or PowerShell. On Windows a
// command is analysed in both and the most restrictive result holds.
type dialect uint8

const (
	dialectPOSIX dialect = iota
	dialectPwsh
)

// word is one word of a simple command.
type word struct {
	text   string // quotes and escapes removed
	quoted bool   // some part was quoted
	expand bool   // holds something only the shell resolves ($x, $(…), `…`)
	glob   bool   // an unquoted *, ? or [
	tilde  bool   // starts with an unquoted ~
	assign int    // index of the = of a leading NAME=value, or -1
}

type redirect struct {
	op     string
	target word
}

// writes reports whether the redirection writes its target.
func (r redirect) writes() bool { return r.op != "<" && r.op != "<<" && r.op != "<<<" }

// simple is one simple command.
type simple struct {
	assigns []word
	words   []word
	redirs  []redirect
	opaque  []string // why it cannot be analysed (it ends in ask at least)
	subs    []string // texts of $(…), `…`, <(…), analysed on their own
	stdin   []string // bodies of its here-documents and here-strings
	next    string   // the separator after it
}

type token struct {
	op  string // an operator; "" for a word
	w   word
	doc *hereDoc // the here-document of a << operator, when it was read
}

type lexer struct {
	s      string
	i      int
	d      dialect
	toks   []token
	opaque []string
	// current word
	buf    strings.Builder
	cur    word
	inWord bool
	// braces are the offsets in buf of the unquoted {, } and , of the
	// current word (POSIX): what a brace expansion is made of. quotes are
	// the quoted parts of the word, which decide whether an empty word of
	// the expansion is kept.
	braces []int
	quotes []quoteSpan
	opens  bool // braces holds an unquoted {
	// pending are the here-documents of the current line, whose bodies
	// start after its newline.
	pending []*hereDoc
}

// quoted records that the text of the current word from offset from on
// came from quotes (POSIX); an empty part still counts.
func (lx *lexer) quoted(from int) {
	lx.quotes = append(lx.quotes, quoteSpan{from, lx.buf.Len()})
}

// parseLine splits a command line into simple commands. opaque holds the
// reasons that apply to the whole line.
func parseLine(s string, d dialect) (parts []simple, opaque []string, grouped bool) {
	lx := &lexer{s: s, d: d}
	lx.run()
	parts, grouped = group(lx.toks, d)
	return parts, lx.opaque, grouped
}

func (lx *lexer) startWord() {
	if !lx.inWord {
		lx.inWord = true
		lx.cur = word{assign: -1}
		lx.buf.Reset()
		lx.braces = lx.braces[:0]
		lx.quotes = lx.quotes[:0]
		lx.opens = false
	}
}

func (lx *lexer) flush() {
	if !lx.inWord {
		return
	}
	lx.inWord = false
	lx.cur.text = lx.buf.String()
	if lx.opens {
		words, ok := braceExpand(lx.cur.text, lx.braces, lx.quotes)
		switch {
		case !ok:
			// Too many words to analyse: the line is denied.
			lx.mark(braceTooLarge)
		case len(words) > 1 || words[0].text != lx.cur.text:
			// The shell runs the words of the expansion: they are analysed
			// as words of the line, which is at least ask.
			lx.opaque = append(lx.opaque, "expansão de chaves")
			first := lx.braces[0]
			for _, bw := range words {
				t := bw.text
				if t == "" && !bw.quoted {
					// An empty word of the expansion is dropped, unless a
					// quoted part ("" or '') went into it: {"git",} push is
					// git push.
					continue
				}
				w := lx.cur
				w.text = t
				if w.assign > first {
					w.assign = -1
				}
				w.tilde = w.tilde || first == 0 && strings.HasPrefix(t, "~")
				lx.toks = append(lx.toks, token{w: w})
			}
			return
		}
	}
	lx.toks = append(lx.toks, token{w: lx.cur})
}

func (lx *lexer) op(o string) {
	lx.flush()
	lx.toks = append(lx.toks, token{op: o})
}

func (lx *lexer) peek(k int) byte {
	if lx.i+k < len(lx.s) {
		return lx.s[lx.i+k]
	}
	return 0
}

func (lx *lexer) has(prefix string) bool { return strings.HasPrefix(lx.s[lx.i:], prefix) }

func (lx *lexer) run() {
	for lx.i < len(lx.s) {
		if lx.d == dialectPwsh {
			lx.stepPwsh()
		} else {
			lx.stepPOSIX()
		}
	}
	lx.flush()
}

// fdPrefix turns a word made only of digits (or * in PowerShell) right
// before a redirection into its file descriptor: it is dropped.
func (lx *lexer) fdPrefix() {
	if !lx.inWord || lx.cur.quoted || lx.cur.expand {
		return
	}
	t := lx.buf.String()
	if t == "" {
		return
	}
	for _, c := range t {
		if (c < '0' || c > '9') && (lx.d != dialectPwsh || c != '*') {
			return
		}
	}
	lx.inWord = false
}

func (lx *lexer) stepPOSIX() {
	c := lx.s[lx.i]
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		lx.flush()
		lx.i++
	case c == '\n':
		lx.op("\n")
		lx.i++
		lx.readBodies()
	case c == '#' && !lx.inWord:
		for lx.i < len(lx.s) && lx.s[lx.i] != '\n' {
			lx.i++
		}
	case c == '\\':
		if lx.peek(1) == '\n' {
			lx.i += 2
			return
		}
		lx.startWord()
		lx.cur.quoted = true
		from := lx.buf.Len()
		if lx.i+1 < len(lx.s) {
			lx.buf.WriteByte(lx.s[lx.i+1])
		}
		lx.quoted(from)
		lx.i += 2
	case c == '\'':
		lx.startWord()
		lx.cur.quoted = true
		from := lx.buf.Len()
		defer lx.quoted(from)
		end := strings.IndexByte(lx.s[lx.i+1:], '\'')
		if end < 0 {
			lx.opaque = append(lx.opaque, "aspas sem fechar")
			lx.buf.WriteString(lx.s[lx.i+1:])
			lx.i = len(lx.s)
			return
		}
		lx.buf.WriteString(lx.s[lx.i+1 : lx.i+1+end])
		lx.i += end + 2
	case c == '"':
		lx.startWord()
		lx.cur.quoted = true
		from := lx.buf.Len()
		lx.i++
		lx.doubleQuoted()
		lx.quoted(from)
	case c == '$':
		lx.startWord()
		from := lx.buf.Len()
		ansi := lx.peek(1) == '\''
		lx.dollar()
		if ansi {
			lx.quoted(from)
		}
	case c == '`':
		lx.startWord()
		lx.backtick()
	case c == ';':
		if lx.peek(1) == ';' {
			lx.op(";;")
			lx.i += 2
			return
		}
		lx.op(";")
		lx.i++
	case c == '&':
		switch {
		case lx.has("&&"):
			lx.op("&&")
			lx.i += 2
		case lx.has("&>>"):
			lx.op("&>>")
			lx.i += 3
		case lx.has("&>"):
			lx.op("&>")
			lx.i += 2
		default:
			lx.op("&")
			lx.i++
		}
	case c == '|':
		switch {
		case lx.has("||"):
			lx.op("||")
			lx.i += 2
		case lx.has("|&"):
			lx.op("|&")
			lx.i += 2
		default:
			lx.op("|")
			lx.i++
		}
	case c == '(' || c == ')':
		lx.op(string(c))
		lx.i++
	case c == '<' || c == '>':
		lx.redirPOSIX(c)
	default:
		lx.startWord()
		if c == '*' || c == '?' || c == '[' {
			lx.cur.glob = true
		}
		if c == '~' && lx.buf.Len() == 0 && !lx.cur.quoted {
			lx.cur.tilde = true
		}
		if c == '=' && lx.cur.assign < 0 && !lx.cur.quoted && !lx.cur.expand && nameRe.MatchString(lx.buf.String()) {
			lx.cur.assign = lx.buf.Len()
		}
		if c == '{' || c == '}' || c == ',' {
			lx.braces = append(lx.braces, lx.buf.Len())
			lx.opens = lx.opens || c == '{'
		}
		lx.buf.WriteByte(c)
		lx.i++
	}
}

var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (lx *lexer) redirPOSIX(c byte) {
	if lx.peek(1) == '(' { // <(…) or >(…): process substitution
		lx.startWord()
		lx.i++
		inner, ok := lx.balanced('(', ')')
		lx.sub(inner, ok, "substituição de processo")
		return
	}
	lx.fdPrefix()
	ops := []string{"<<<", "<<-", "<<", "<>", "<&", ">>", ">|", ">&", "<", ">"}
	for _, o := range ops {
		if lx.has(o) {
			lx.i += len(o)
			switch o {
			case "<<", "<<-":
				lx.hereDoc(lx.i-len(o), o == "<<-")
			case "<&", ">&":
				// fd duplication (2>&1, >&-) has no file; >&file writes it.
				j := lx.i
				for j < len(lx.s) && lx.s[j] == ' ' {
					j++
				}
				if j < len(lx.s) && (lx.s[j] == '-' || (lx.s[j] >= '0' && lx.s[j] <= '9')) {
					for j < len(lx.s) && lx.s[j] != ' ' && lx.s[j] != ';' && lx.s[j] != '&' && lx.s[j] != '|' && lx.s[j] != '\n' {
						j++
					}
					lx.flush()
					lx.i = j
					return
				}
				if o == ">&" {
					lx.op(">")
				} else {
					lx.op("<")
				}
			default:
				lx.op(o)
			}
			return
		}
	}
}

func (lx *lexer) doubleQuoted() {
	for lx.i < len(lx.s) {
		c := lx.s[lx.i]
		switch c {
		case '"':
			lx.i++
			return
		case '\\':
			n := lx.peek(1)
			if n == '$' || n == '`' || n == '"' || n == '\\' {
				lx.buf.WriteByte(n)
				lx.i += 2
				continue
			}
			if n == '\n' {
				lx.i += 2
				continue
			}
			lx.buf.WriteByte(c)
			lx.i++
		case '$':
			lx.dollar()
		case '`':
			lx.backtick()
		default:
			lx.buf.WriteByte(c)
			lx.i++
		}
	}
	lx.opaque = append(lx.opaque, "aspas sem fechar")
}

// dollar reads an expansion that starts at lx.i ($).
func (lx *lexer) dollar() {
	n := lx.peek(1)
	switch {
	case n == '(' && lx.peek(2) == '(':
		// $((…)): arithmetic, not a command; it only makes the line opaque.
		lx.i += 2
		inner, ok := lx.balanced('(', ')')
		lx.mark("expansão aritmética")
		lx.buf.WriteString("$((" + inner + ")")
		if lx.i < len(lx.s) && lx.s[lx.i] == ')' {
			lx.i++
		}
		if !ok {
			lx.opaque = append(lx.opaque, "expansão sem fechar")
		}
	case n == '(':
		lx.i++
		inner, ok := lx.balanced('(', ')')
		lx.sub(inner, ok, "substituição de comando $(…)")
	case n == '{':
		lx.i++
		inner, ok := lx.balanced('{', '}')
		lx.mark("expansão de variável")
		lx.buf.WriteString("${" + inner + "}")
		if !ok {
			lx.opaque = append(lx.opaque, "expansão sem fechar")
		}
	case n == '\'':
		// $'…': ANSI-C quoting; its escapes can spell anything.
		lx.mark("aspas $'…'")
		lx.i += 2
		end := strings.IndexByte(lx.s[lx.i:], '\'')
		if end < 0 {
			lx.i = len(lx.s)
			return
		}
		lx.buf.WriteString(lx.s[lx.i : lx.i+end])
		lx.i += end + 1
	case isNameByte(n) || strings.IndexByte("?!#@*-$0123456789", n) >= 0:
		lx.mark("expansão de variável")
		j := lx.i + 1
		if isNameByte(n) {
			for j < len(lx.s) && isNameByte(lx.s[j]) {
				j++
			}
		} else {
			j++
		}
		lx.buf.WriteString(lx.s[lx.i:j])
		lx.i = j
	default:
		lx.buf.WriteByte('$')
		lx.i++
	}
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (lx *lexer) backtick() {
	lx.i++
	j := lx.i
	for j < len(lx.s) && lx.s[j] != '`' {
		if lx.s[j] == '\\' {
			j++
		}
		j++
	}
	ok := j < len(lx.s)
	inner := lx.s[lx.i:min(j, len(lx.s))]
	lx.i = min(j+1, len(lx.s))
	lx.sub(inner, ok, "substituição de comando com crases")
}

func (lx *lexer) mark(reason string) {
	lx.cur.expand = true
	lx.opaque = append(lx.opaque, reason)
}

func (lx *lexer) sub(inner string, ok bool, reason string) {
	lx.mark(reason)
	lx.buf.WriteString("$(" + inner + ")")
	lx.toks = append(lx.toks, token{op: "sub", w: word{text: inner}})
	if !ok {
		lx.opaque = append(lx.opaque, "substituição sem fechar")
	}
}

// balanced reads up to the close that matches the open just before lx.i,
// skipping quoted text; lx.i ends after the close.
func (lx *lexer) balanced(open, close byte) (string, bool) {
	lx.i++ // past the open
	start, depth := lx.i, 1
	for lx.i < len(lx.s) {
		c := lx.s[lx.i]
		switch c {
		case '\\':
			lx.i++
		case '\'', '"':
			end := strings.IndexByte(lx.s[lx.i+1:], c)
			if end < 0 {
				lx.i = len(lx.s)
				return lx.s[start:], false
			}
			lx.i += end + 1
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				inner := lx.s[start:lx.i]
				lx.i++
				return inner, true
			}
		}
		lx.i++
	}
	return lx.s[start:], false
}

func (lx *lexer) stepPwsh() {
	c := lx.s[lx.i]
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		lx.flush()
		lx.i++
	case c == '\n':
		lx.op("\n")
		lx.i++
	case lx.has("<#"):
		end := strings.Index(lx.s[lx.i:], "#>")
		if end < 0 {
			lx.i = len(lx.s)
			return
		}
		lx.i += end + 2
	case c == '#' && !lx.inWord:
		for lx.i < len(lx.s) && lx.s[lx.i] != '\n' {
			lx.i++
		}
	case c == '`':
		if lx.peek(1) == '\n' {
			lx.i += 2
			return
		}
		lx.startWord()
		lx.cur.quoted = true
		if lx.i+1 < len(lx.s) {
			lx.buf.WriteByte(lx.s[lx.i+1])
		}
		lx.i += 2
	case c == '\'':
		lx.startWord()
		lx.cur.quoted = true
		lx.i++
		for lx.i < len(lx.s) {
			if lx.s[lx.i] == '\'' {
				if lx.peek(1) == '\'' {
					lx.buf.WriteByte('\'')
					lx.i += 2
					continue
				}
				lx.i++
				return
			}
			lx.buf.WriteByte(lx.s[lx.i])
			lx.i++
		}
		lx.opaque = append(lx.opaque, "aspas sem fechar")
	case c == '"':
		lx.startWord()
		lx.cur.quoted = true
		lx.i++
		lx.pwshDouble()
	case c == '$':
		lx.startWord()
		lx.pwshDollar()
	case c == '@' && (lx.peek(1) == '(' || lx.peek(1) == '{' || isNameByte(lx.peek(1))) && !lx.inWord:
		lx.startWord()
		lx.mark("expressão do PowerShell")
		lx.buf.WriteByte('@')
		lx.i++
	case c == ';':
		lx.op(";")
		lx.i++
	case c == '&':
		if lx.has("&&") {
			lx.op("&&")
			lx.i += 2
			return
		}
		lx.op("&")
		lx.i++
	case c == '|':
		if lx.has("||") {
			lx.op("||")
			lx.i += 2
			return
		}
		lx.op("|")
		lx.i++
	case c == '(' || c == ')' || c == '{' || c == '}':
		lx.op(string(c))
		lx.i++
	case c == '>' || c == '<':
		lx.fdPrefix()
		if lx.has(">&") || lx.has("<&") {
			// 2>&1: stream merge, no file
			lx.flush()
			lx.i += 2
			for lx.i < len(lx.s) && lx.s[lx.i] >= '0' && lx.s[lx.i] <= '9' {
				lx.i++
			}
			return
		}
		if lx.has(">>") {
			lx.op(">>")
			lx.i += 2
			return
		}
		lx.op(string(c))
		lx.i++
	default:
		lx.startWord()
		if c == '*' || c == '?' || c == '[' {
			lx.cur.glob = true
		}
		if c == '~' && lx.buf.Len() == 0 && !lx.cur.quoted {
			lx.cur.tilde = true
		}
		lx.buf.WriteByte(c)
		lx.i++
	}
}

func (lx *lexer) pwshDouble() {
	for lx.i < len(lx.s) {
		c := lx.s[lx.i]
		switch c {
		case '"':
			if lx.peek(1) == '"' {
				lx.buf.WriteByte('"')
				lx.i += 2
				continue
			}
			lx.i++
			return
		case '`':
			if lx.i+1 < len(lx.s) {
				lx.buf.WriteByte(lx.s[lx.i+1])
			}
			lx.i += 2
		case '$':
			lx.pwshDollar()
		default:
			lx.buf.WriteByte(c)
			lx.i++
		}
	}
	lx.opaque = append(lx.opaque, "aspas sem fechar")
}

func (lx *lexer) pwshDollar() {
	n := lx.peek(1)
	switch {
	case n == '(':
		lx.i++
		inner, ok := lx.balanced('(', ')')
		lx.sub(inner, ok, "subexpressão $(…)")
	case n == '{':
		lx.i++
		inner, _ := lx.balanced('{', '}')
		lx.mark("variável do PowerShell")
		lx.buf.WriteString("${" + inner + "}")
	case isNameByte(n) || n == '_' || n == '?' || n == '$' || n == '^':
		j := lx.i + 1
		for j < len(lx.s) && (isNameByte(lx.s[j]) || lx.s[j] == ':') {
			j++
		}
		if j == lx.i+1 {
			j++
		}
		name := lx.s[lx.i:j]
		if strings.EqualFold(name, "$null") {
			lx.buf.WriteString("$null") // the null device, not a variable to resolve
			lx.i = j
			return
		}
		lx.mark("variável do PowerShell")
		lx.buf.WriteString(name)
		lx.i = j
	default:
		lx.buf.WriteByte('$')
		lx.i++
	}
}

var (
	posixKeywords  = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "fi": true, "do": true, "done": true, "!": true, "while": true, "until": true, "{": true, "}": true, "esac": true}
	posixOpaque    = map[string]bool{"for": true, "case": true, "select": true, "function": true, "coproc": true}
	pwshKeywords   = map[string]bool{"if": true, "elseif": true, "else": true, "while": true, "do": true, "until": true, "try": true, "catch": true, "finally": true, "foreach": true, "for": true, "return": true, "throw": true, "exit": true, "break": true, "continue": true, "begin": true, "process": true, "end": true}
	pwshOpaqueWord = map[string]bool{"function": true, "filter": true, "param": true, "switch": true, "class": true, "enum": true, "trap": true, "workflow": true, "data": true}
)

// group splits the tokens at the separators. grouped is true when the line
// has subshells, braces, pipes or background jobs, where a cd does not
// surely hold for the next part.
func group(toks []token, d dialect) (parts []simple, grouped bool) {
	var cur simple
	end := func(op string) {
		if len(cur.words) > 0 || len(cur.redirs) > 0 || len(cur.subs) > 0 {
			cur.next = op
			parts = append(parts, cur)
		}
		cur = simple{}
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.op {
		case "":
			w := t.w
			if d == dialectPOSIX && !w.quoted && !w.expand && (w.text == "{" || w.text == "}") {
				grouped = true
				end(w.text)
				continue
			}
			cur.words = append(cur.words, w)
		case "sub":
			cur.subs = append(cur.subs, t.w.text)
		case ";", ";;", "&&", "||", "|", "|&", "&", "\n", "(", ")", "{", "}":
			switch t.op {
			case "|", "|&", "&", "(", ")", "{", "}":
				grouped = true
			}
			end(t.op)
		default: // a redirection
			if t.doc != nil && t.doc.read {
				cur.stdin = append(cur.stdin, t.doc.body)
				if !t.doc.quoted {
					// The shell expands $(…) and `…` in the body.
					subs, why := bodyExpansions(t.doc.body)
					cur.subs = append(cur.subs, subs...)
					cur.opaque = append(cur.opaque, why...)
				}
			}
			// Substitutions inside the target belong to this part.
			for i+1 < len(toks) && toks[i+1].op == "sub" {
				cur.subs = append(cur.subs, toks[i+1].w.text)
				i++
			}
			if i+1 < len(toks) && toks[i+1].op == "" {
				if t.op == "<<<" {
					cur.stdin = append(cur.stdin, toks[i+1].w.text)
				}
				cur.redirs = append(cur.redirs, redirect{op: t.op, target: toks[i+1].w})
				i++
				for i+1 < len(toks) && toks[i+1].op == "sub" {
					cur.subs = append(cur.subs, toks[i+1].w.text)
					i++
				}
				continue
			}
			cur.opaque = append(cur.opaque, "redirecionamento sem destino")
		}
	}
	end("")
	kept := parts[:0]
	for _, p := range parts {
		p.finish(d)
		if len(p.words)+len(p.assigns)+len(p.redirs)+len(p.subs)+len(p.opaque) > 0 {
			kept = append(kept, p) // a part of keywords only (fi, done) is dropped
		}
	}
	return kept, grouped
}

// finish separates the leading assignments and keywords from the command
// and records what makes the part opaque.
func (p *simple) finish(d dialect) {
	words := p.words
	for len(words) > 0 {
		w := words[0]
		lower := strings.ToLower(w.text)
		switch {
		case d == dialectPOSIX && !w.quoted && !w.expand && posixKeywords[w.text]:
			words = words[1:]
		case d == dialectPOSIX && !w.quoted && posixOpaque[w.text]:
			p.opaque = append(p.opaque, "laço ou função do shell")
			words = words[1:]
		case d == dialectPOSIX && w.assign > 0:
			p.assigns = append(p.assigns, w)
			words = words[1:]
		case d == dialectPwsh && !w.quoted && pwshKeywords[lower]:
			words = words[1:]
		case d == dialectPwsh && !w.quoted && pwshOpaqueWord[lower]:
			p.opaque = append(p.opaque, "bloco do PowerShell")
			words = words[1:]
		default:
			p.words = words
			p.scan(d)
			return
		}
	}
	p.words = nil
	p.scan(d)
}

func (p *simple) scan(d dialect) {
	for _, w := range p.words {
		if w.expand {
			p.opaque = append(p.opaque, "expansão que só o shell resolve")
			break
		}
	}
	if d == dialectPwsh {
		for _, w := range p.words {
			if w.text == "--%" {
				p.opaque = append(p.opaque, "--% do PowerShell")
			}
			if strings.HasPrefix(w.text, "[") && strings.Contains(w.text, "]::") {
				p.opaque = append(p.opaque, "chamada .NET")
			}
		}
	}
	for _, r := range p.redirs {
		if r.target.expand && (d != dialectPwsh || !strings.EqualFold(r.target.text, "$null")) {
			p.opaque = append(p.opaque, "redirecionamento para expansão")
		}
	}
}

// texts returns the words as text.
func (p *simple) texts() []string {
	out := make([]string, len(p.words))
	for i, w := range p.words {
		out[i] = w.text
	}
	return out
}
