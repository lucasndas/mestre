package inbox

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The words in a text the engine types that somebody else chose (the sender of an
// item, the text of an agent, a path) must never pass for the engine's own: its
// header, its fields and the end of a block. The terminal gets the text after the
// typing layer drops the control characters (internal/providers/typing.go), so
// what is checked here is what a reader sees: nothing here depends on a control
// character staying where it was.

// headerText makes words that somebody chose safe inside the header: no line
// break, control character or invisible mark (each run of them becomes a space),
// no bracket (every opening one becomes `(`, every closing one `)`) and nothing
// that looks like the middle dot of the format (it becomes `-`). Nothing written
// in a sender can end the header, break the line or open another header on it
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
func headerText(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp):
			gap = true
			continue
		case unicode.Is(unicode.Ps, r):
			r = '('
		case unicode.Is(unicode.Pe, r):
			r = ')'
		case isDot(r):
			r = '-'
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// PrintableLine says whether s is one line of printable characters, with no line
// break, control character or invisible mark. A name that becomes the sender of
// an item (of a quest, of a ritual) must be, so whoever names it does not get to
// write a line of the engine; Header cleans what is already named otherwise.
func PrintableLine(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) || isFiller(r) })
}

// OneLine keeps words that somebody chose (a path, a command, a URL) on their
// own line in a text of the engine: a text with a line break or another control
// character is shown quoted and escaped, so it cannot open a line that looks
// like a field of the engine, a header or the end of a block of text of an agent.
func OneLine(s string) string {
	for _, r := range s {
		if unicode.IsControl(r) || r == '\U00002028' || r == '\U00002029' {
			return strconv.Quote(s)
		}
	}
	return s
}

// quotePrefix starts every line Quote shows.
const quotePrefix = "|"

// breaks turn every break a reader may take for the end of a line into the one
// the lines are split at.
var breaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n",
	"\U00000085", "\n", "\U00002028", "\n", "\U00002029", "\n")

// Quote shows words that somebody else wrote inside a text of the engine: every
// line of them starts with "| ", the empty ones with "|". None can start at the
// left margin, where the engine writes its fields, its headers and the end of a
// block of text of an agent, whatever the words are made of: the prefix is put
// after the text is split, so no character in a line takes it away. A break the
// reader may take for a line break is one here too.
func Quote(text string) string { return QuoteWith(quotePrefix, text) }

// QuoteWith is Quote with another mark (">" for a note in markdown).
func QuoteWith(mark, text string) string {
	text = strings.TrimRight(breaks.Replace(text), "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = mark
		} else {
			lines[i] = mark + " " + l
		}
	}
	return strings.Join(lines, "\n")
}

// Say writes the words of somebody else after a label of the engine: on the same
// line when they are one line, so a short answer reads as it always did, and
// otherwise the label ends its line and the words follow it, each line quoted with
// the mark. What the engine writes after them cannot be imitated by them.
func Say(label, text, mark string) string {
	text = strings.TrimRight(breaks.Replace(text), "\n")
	switch {
	case text == "":
		return label
	case !strings.Contains(text, "\n"):
		return label + " " + text
	}
	return label + "\n" + QuoteWith(mark, text)
}

// EscapeHeaders makes a line that imitates a header harmless: the text of an
// item that is not from a user may carry words of an agent, and a line
// `[Mestre · …]` in it must not read as the header of another item. The line keeps
// its words, behind a backslash. A line is what comes between the breaks a
// reader may take for one, and it imitates a header when, with what shows nothing
// taken out, it starts with an opening bracket, "Mestre" and a dot, written with
// the letters, brackets and dots of other scripts that look the same too
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
func EscapeHeaders(text string) string {
	var b strings.Builder
	b.Grow(len(text) + 8)
	for text != "" {
		line, brk, rest := text, "", ""
		if i := strings.IndexFunc(text, isLineBreak); i >= 0 {
			_, n := utf8.DecodeRuneInString(text[i:])
			line, brk, rest = text[:i], text[i:i+n], text[i+n:]
		}
		if at := headerMarkAt(line); at >= 0 {
			b.WriteString(line[:at] + `\` + line[at:])
		} else {
			b.WriteString(line)
		}
		b.WriteString(brk)
		text = rest
	}
	return b.String()
}

// headerMarkAt says where the opening bracket of a mark that looks like the start
// of a header is in the line, or -1: after what shows nothing, an opening bracket,
// the letters of "mestre" and a dot, with what shows nothing allowed between them.
func headerMarkAt(line string) int {
	i := skipBlank(line, 0)
	open := i
	r, n := utf8.DecodeRuneInString(line[i:])
	if n == 0 || !unicode.Is(unicode.Ps, r) {
		return -1
	}
	i += n
	for _, want := range "mestre" {
		i = skipBlank(line, i)
		r, n = utf8.DecodeRuneInString(line[i:])
		if n == 0 || skeleton(r) != want {
			return -1
		}
		i += n
	}
	i = skipBlank(line, i)
	if r, n = utf8.DecodeRuneInString(line[i:]); n == 0 || !isDot(r) {
		return -1
	}
	return open
}

// skipBlank returns the index of the first rune of line from i on that shows
// something.
func skipBlank(line string, i int) int {
	for i < len(line) {
		r, n := utf8.DecodeRuneInString(line[i:])
		if !blank(r) {
			break
		}
		i += n
	}
	return i
}

// blank says whether r shows nothing to a reader: a space, a control character,
// a mark of format or a combining mark, or one of the letters that are drawn
// empty.
func blank(r rune) bool {
	return unicode.In(r, unicode.Z, unicode.Cc, unicode.Cf, unicode.Mn, unicode.Me) || isFiller(r)
}

// isFiller says whether r is a letter or a sign that is drawn empty (Hangul
// fillers, the blank braille pattern).
func isFiller(r rune) bool {
	switch r {
	case '\U0000115F', '\U00001160', '\U00003164', '\U0000FFA0', '\U00002800':
		return true
	}
	return false
}

// dots are the characters that stand for the middle dot of the header: the
// engine writes U+00B7, and these look like it.
var dots = []rune{'\U000000B7', '\U00000387', '\U00002022', '\U00002024', '\U00002027', '\U00002219', '\U000022C5',
	'\U00002E31', '\U000030FB', '\U0000FF65', '\U0000A78F'}

func isDot(r rune) bool { return slices.Contains(dots, r) }

// lookalikes are the letters of other scripts a reader takes for the Latin ones
// of "mestre".
var lookalikes = map[rune]rune{
	'\U0000041C': 'm', '\U0000043C': 'm', '\U0000039C': 'm', '\U00001D0D': 'm', // М м Μ ᴍ
	'\U00000435': 'e', '\U00000415': 'e', '\U00000395': 'e', '\U00000454': 'e', '\U00001D07': 'e', // е Е Ε є ᴇ
	'\U00000455': 's', '\U00000405': 's', '\U0000A731': 's', // ѕ Ѕ ꜱ
	'\U00000422': 't', '\U000003A4': 't', '\U000003C4': 't', '\U00001D1B': 't', // Т Τ τ ᴛ
	'\U00000433': 'r', '\U00000280': 'r', '\U0000027E': 'r', // г ʀ ɾ
}

// skeleton is the lower-case Latin letter a reader takes r for, or r itself:
// full-width letters and the lookalikes of other scripts count as the letters.
func skeleton(r rune) rune {
	switch {
	case r >= '\U0000FF21' && r <= '\U0000FF3A':
		r = r - '\U0000FF21' + 'A'
	case r >= '\U0000FF41' && r <= '\U0000FF5A':
		r = r - '\U0000FF41' + 'a'
	}
	if l, ok := lookalikes[r]; ok {
		return l
	}
	return unicode.ToLower(r)
}

// isLineBreak says whether a reader may take r for the end of a line.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\U00000085', '\U00002028', '\U00002029':
		return true
	}
	return false
}
