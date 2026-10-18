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

// headerText makes words that somebody chose safe inside the header. Only what a
// name is made of gets through: letters (but not the ones drawn empty), numbers and
// combining marks of any script, and the printable ASCII signs. Every run of
// anything else (spaces, line breaks, control characters, invisible marks, and the
// signs and symbols of other scripts, which are where the look-alikes of the
// brackets and of the middle dot of the format live) becomes one space; every
// opening bracket becomes `(`, every closing one `)`, and a dot-like sign `-`. So
// nothing written in a sender can end the header, break the line or open another
// header on it, and no sign of another script stands in for one of the format
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada). A letter
// drawn like a bracket or a dot is still a letter: the sender cannot make the
// format, only look like a name.
func headerText(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		switch {
		case isDot(r):
			r = '-'
		case r == '\U0000201A' || r == '\U0000201E' || r == '\U00002E42':
			// opening by category, but quotation marks: they group nothing
			gap = true
			continue
		case unicode.Is(unicode.Ps, r):
			r = '('
		case unicode.Is(unicode.Pe, r):
			r = ')'
		case !namePart(r):
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// namePart says whether r may stay in a field of the header.
func namePart(r rune) bool {
	if r < utf8.RuneSelf {
		return r > ' ' && r != '\x7f'
	}
	return (unicode.IsLetter(r) && !isFiller(r)) || unicode.IsNumber(r) || unicode.IsMark(r)
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
func Quote(text string) string {
	text = strings.TrimRight(breaks.Replace(text), "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = quotePrefix
		} else {
			lines[i] = quotePrefix + " " + l
		}
	}
	return strings.Join(lines, "\n")
}

// EscapeHeaders makes a line that imitates a header harmless: the text of an
// item that is not from a user may carry words of an agent, and a line
// `[Mestre · …]` in it must not read as the header of another item. The line keeps
// its words, behind a backslash. A line is what comes between the breaks a
// reader may take for one, and it imitates a header when, with what shows nothing
// taken out, it starts with an opening bracket, "Mestre" and a dot or a dash,
// written with the letters, brackets and dots of other scripts that look the same
// too. It is a best effort: the list of what looks the same is the one in this
// file, and no list is complete, so the fields of the engine do not rely on it
// (they use Quote) (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
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
// the letters of "mestre" and a dot or a dash, with what shows nothing allowed
// between them.
func headerMarkAt(line string) int {
	i := skipBlank(line, 0)
	open := i
	r, n := utf8.DecodeRuneInString(line[i:])
	if n == 0 || !isOpening(r) {
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
	if r, n = utf8.DecodeRuneInString(line[i:]); n == 0 || (!isDot(r) && !unicode.Is(unicode.Pd, r)) {
		return -1
	}
	return open
}

// isOpening says whether r looks like the bracket that opens the header: an
// opening bracket or quote of any script, the angle bracket, the pieces of a
// large bracket, or a letter drawn like one.
func isOpening(r rune) bool {
	return unicode.In(r, unicode.Ps, unicode.Pi) || r == '<' || (r >= '\U0000239B' && r <= '\U000023AD') || r == '\U000014A5'
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
	'\U00002E31', '\U000030FB', '\U0000FF65', '\U0000A78F', '\U00001427', '\U000016EB', '\U00002981', '\U00002E30',
	'\U00002E33', '\U00000F0B'}

func isDot(r rune) bool { return slices.Contains(dots, r) }

// lookalikes are the letters of other scripts a reader takes for the Latin ones
// of "mestre".
var lookalikes = map[rune]rune{
	'\U0000041C': 'm', '\U0000043C': 'm', '\U0000039C': 'm', '\U00001D0D': 'm', // М м Μ ᴍ
	'\U00000435': 'e', '\U00000415': 'e', '\U00000395': 'e', '\U00000454': 'e', '\U00001D07': 'e', // е Е Ε є ᴇ
	'\U00000455': 's', '\U00000405': 's', '\U0000A731': 's', // ѕ Ѕ ꜱ
	'\U00000422': 't', '\U00000442': 't', '\U000003A4': 't', '\U000003C4': 't', '\U00001D1B': 't', // Т т Τ τ ᴛ
	'\U00000433': 'r', '\U00000280': 'r', '\U0000027E': 'r', // г ʀ ɾ
	'\U0000212F': 'e', '\U00002130': 'e', '\U00002133': 'm', '\U0000211B': 'r', '\U0000211C': 'r', '\U0000211D': 'r', // ℯ ℰ ℳ ℛ ℜ ℝ
	'\U0000A4DF': 'm', '\U0000A4F0': 'e', '\U0000A4E2': 's', '\U0000A4D4': 't', '\U0000A4E3': 'r', // Lisu
	'\U000013B7': 'm', '\U000013AC': 'e', '\U000013DA': 's', '\U000013A2': 't', '\U000013D2': 'r', // Cherokee
}

// accents are the letters of "mestre" with a mark that has a code point of its
// own (é, ś, ț, ṙ, ṁ…), by the letter they are drawn from; the combining marks
// are already skipped as blank.
var accents = accentTable()

func accentTable() map[rune]rune {
	t := map[rune]rune{}
	for base, from := range map[rune][]rune{
		'm': {0x1E3F, 0x1E41, 0x1E43},
		'e': {0xE8, 0xE9, 0xEA, 0xEB, 0x113, 0x115, 0x117, 0x119, 0x11B, 0x205, 0x207, 0x229, 0x1E15, 0x1E17, 0x1E19, 0x1E1B, 0x1E1D,
			0x1EB9, 0x1EBB, 0x1EBD, 0x1EBF, 0x1EC1, 0x1EC3, 0x1EC5, 0x1EC7},
		's': {0x15B, 0x15D, 0x15F, 0x161, 0x219, 0x1E61, 0x1E63, 0x1E65, 0x1E67, 0x1E69},
		't': {0x163, 0x165, 0x167, 0x21B, 0x1E6B, 0x1E6D, 0x1E6F, 0x1E71, 0x1E97},
		'r': {0x155, 0x157, 0x159, 0x211, 0x213, 0x1E59, 0x1E5B, 0x1E5D, 0x1E5F},
	} {
		for _, r := range from {
			t[r] = base
		}
	}
	return t
}

// latin is the Latin letter an alphabet that copies the Latin one draws r as:
// full-width, mathematical (bold, italic, script, fraktur, double-struck, sans,
// monospace: 52 letters a style), circled and parenthesized letters, and the Roman
// numeral for a thousand. Any other rune is itself.
func latin(r rune) rune {
	switch {
	case r >= '\U0000FF21' && r <= '\U0000FF3A':
		return r - '\U0000FF21' + 'A'
	case r >= '\U0000FF41' && r <= '\U0000FF5A':
		return r - '\U0000FF41' + 'a'
	case r >= '\U0001D400' && r <= '\U0001D6A3':
		i := (r - '\U0001D400') % 52
		if i < 26 {
			return 'A' + i
		}
		return 'a' + i - 26
	case r >= '\U000024B6' && r <= '\U000024CF':
		return r - '\U000024B6' + 'A'
	case r >= '\U000024D0' && r <= '\U000024E9':
		return r - '\U000024D0' + 'a'
	case r >= '\U0000249C' && r <= '\U000024B5':
		return r - '\U0000249C' + 'a'
	case r == '\U0000216F':
		return 'M'
	case r == '\U0000217F':
		return 'm'
	}
	return r
}

// skeleton is the lower-case Latin letter a reader takes r for, or r itself.
func skeleton(r rune) rune {
	r = latin(r)
	if l, ok := lookalikes[r]; ok {
		return l
	}
	r = unicode.ToLower(r)
	if l, ok := accents[r]; ok {
		return l
	}
	return r
}

// isLineBreak says whether a reader may take r for the end of a line.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\U00000085', '\U00002028', '\U00002029':
		return true
	}
	return false
}
