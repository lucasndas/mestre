package classes

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// PowerShell reads some characters that a POSIX shell does not: the language
// specification takes every space separator (the no-break space, the em
// space, the ideographic space…), the vertical tab and the form feed for
// white space, the typographic single and double quotation marks for quotes
// and the en dash, the em dash and the horizontal bar for the dash of a
// parameter. A line that spells git ‘push’ or git<no-break space>push runs
// git push there, so the PowerShell reading of a line reads them the same.

func isPwshSpace(r rune) bool {
	return r == '\t' || r == '\v' || r == '\f' || r == '\u0085' || unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp)
}

func isPwshSingleQuote(r rune) bool {
	return r == '\'' || r == '‘' || r == '’' || r == '‚' || r == '‛'
}

func isPwshDoubleQuote(r rune) bool {
	return r == '"' || r == '“' || r == '”' || r == '„'
}

func isPwshDash(r rune) bool { return r == '-' || r == '–' || r == '—' || r == '―' }

// pwshDashFix is a word that starts with a dash of PowerShell, written with
// the plain hyphen.
func pwshDashFix(s string) string {
	if r, n := utf8.DecodeRuneInString(s); n > 1 && isPwshDash(r) {
		return "-" + s[n:]
	}
	return s
}

// pwshWide reads a non-ASCII character that PowerShell takes for a space, a
// quote or a dash; it reports false for any other.
func (lx *lexer) pwshWide() bool {
	r, n := utf8.DecodeRuneInString(lx.s[lx.i:])
	switch {
	case isPwshSpace(r):
		lx.flush()
		lx.i += n
	case isPwshSingleQuote(r):
		lx.startWord()
		lx.cur.quoted = true
		lx.i += n
		lx.pwshSingle()
	case isPwshDoubleQuote(r):
		lx.startWord()
		lx.cur.quoted = true
		lx.i += n
		lx.pwshDouble()
	case isPwshDash(r) && !lx.inWord:
		lx.startWord()
		lx.buf.WriteByte('-')
		lx.i += n
	default:
		return false
	}
	return true
}

// pwshSingle reads a single-quoted string up to the quote that closes it
// (lx.i is past the one that opened it): any single quotation mark closes
// it, and two together are one quote in the text.
func (lx *lexer) pwshSingle() {
	for lx.i < len(lx.s) {
		r, n := utf8.DecodeRuneInString(lx.s[lx.i:])
		if isPwshSingleQuote(r) {
			if lx.i+n < len(lx.s) {
				if r2, n2 := utf8.DecodeRuneInString(lx.s[lx.i+n:]); isPwshSingleQuote(r2) {
					lx.buf.WriteByte('\'')
					lx.i += n + n2
					continue
				}
			}
			lx.i += n
			return
		}
		lx.buf.WriteString(lx.s[lx.i : lx.i+n])
		lx.i += n
	}
	lx.opaque = append(lx.opaque, "aspas sem fechar")
}

// pwshArgPosition reports a PowerShell part that already has a command: the
// words so far are not only keywords (if, while…), so a parenthesis after
// them opens an argument.
func pwshArgPosition(words []word) bool {
	for _, w := range words {
		if w.quoted || !pwshKeywords[strings.ToLower(w.text)] {
			return true
		}
	}
	return false
}
