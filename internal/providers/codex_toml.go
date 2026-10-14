package providers

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The config.toml of a Codex profile is the provider's own file: Codex
// rewrites it, and the person may keep comments and other tables in it
// (docs/provedores/codex.md#confiança-do-projeto). The Mestre edits it the
// way the contract asks, merging only its part: one key of one table, every
// other byte kept as it was. The editor reads the file as TOML only as far
// as it needs to find tables and keys (headers, dotted keys, strings on one
// or several lines, arrays and inline tables across lines), and refuses a
// file where the table it would add is already defined another way, rather
// than write a file Codex would not load.

// tomlString encodes s as a TOML basic string: quotes, backslashes and
// control characters escaped, everything else (accents included) as UTF-8.
func tomlString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("toml string: text is not valid UTF-8")
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

// tomlKey is a key segment: bare when it can be, a basic string otherwise.
func tomlKey(s string) string {
	bare := s != ""
	for _, r := range s {
		if !isBareKey(r) {
			bare = false
			break
		}
	}
	if bare {
		return s
	}
	q, err := tomlString(s)
	if err != nil { // the callers pass paths and ids, always UTF-8
		return strconv.Quote(s)
	}
	return q
}

func isBareKey(r rune) bool {
	return r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

type tomlKind int

const (
	tomlOther tomlKind = iota // blank line or comment
	tomlHeader
	tomlKeyValue
)

// tomlItem is one logical line of the file: a header, a key/value (which
// may span lines) or a blank or comment line. start and end are byte
// offsets, end past the line break.
type tomlItem struct {
	kind       tomlKind
	array      bool     // [[header]]
	path       []string // the header's table, or the key's dotted path
	value      string   // the value as written, without its comment
	start, end int
}

// scanTOML splits a document into items.
func scanTOML(doc string) ([]tomlItem, error) {
	var items []tomlItem
	for pos := 0; pos < len(doc); {
		lineEnd := endOfLine(doc, pos)
		line := doc[pos:lineEnd]
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.TrimSpace(trimmed) == "" || trimmed[0] == '#':
			items = append(items, tomlItem{kind: tomlOther, start: pos, end: lineEnd})
			pos = lineEnd
		case trimmed[0] == '[':
			it, err := scanHeader(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber(doc, pos), err)
			}
			it.start, it.end = pos, lineEnd
			items = append(items, it)
			pos = lineEnd
		default:
			path, rest, err := parseKeyPath(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber(doc, pos), err)
			}
			if !strings.HasPrefix(rest, "=") {
				return nil, fmt.Errorf("line %d: key without =", lineNumber(doc, pos))
			}
			valStart := pos + (len(line) - len(trimmed)) + (len(trimmed) - len(rest)) + 1
			valEnd, end, err := scanValue(doc, valStart)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber(doc, pos), err)
			}
			items = append(items, tomlItem{kind: tomlKeyValue, path: path, value: strings.TrimSpace(doc[valStart:valEnd]), start: pos, end: end})
			pos = end
		}
	}
	return items, nil
}

func endOfLine(doc string, pos int) int {
	if i := strings.IndexByte(doc[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(doc)
}

func lineNumber(doc string, pos int) int { return strings.Count(doc[:pos], "\n") + 1 }

func scanHeader(line string) (tomlItem, error) {
	it := tomlItem{kind: tomlHeader}
	s := line[1:]
	if strings.HasPrefix(s, "[") {
		it.array, s = true, s[1:]
	}
	path, rest, err := parseKeyPath(s)
	if err != nil {
		return it, err
	}
	closing := "]"
	if it.array {
		closing = "]]"
	}
	if !strings.HasPrefix(rest, closing) {
		return it, errors.New("table header without its closing bracket")
	}
	rest = strings.TrimSpace(rest[len(closing):])
	if rest != "" && rest[0] != '#' {
		return it, errors.New("text after a table header")
	}
	it.path = path
	return it, nil
}

// parseKeyPath reads a dotted key (bare, "basic" or 'literal' segments) and
// returns the segments and the text after it, without leading blanks.
func parseKeyPath(s string) ([]string, string, error) {
	var path []string
	for {
		s = strings.TrimLeft(s, " \t")
		var seg string
		switch {
		case strings.HasPrefix(s, `"`):
			end := closingQuote(s)
			if end < 0 {
				return nil, "", errors.New("unterminated quoted key")
			}
			v, err := decodeBasic(s[1:end])
			if err != nil {
				return nil, "", err
			}
			seg, s = v, s[end+1:]
		case strings.HasPrefix(s, "'"):
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return nil, "", errors.New("unterminated quoted key")
			}
			seg, s = s[1:end+1], s[end+2:]
		default:
			n := 0
			for n < len(s) && isBareKey(rune(s[n])) {
				n++
			}
			if n == 0 {
				return nil, "", errors.New("empty key")
			}
			seg, s = s[:n], s[n:]
		}
		path = append(path, seg)
		s = strings.TrimLeft(s, " \t")
		if !strings.HasPrefix(s, ".") {
			return path, s, nil
		}
		s = s[1:]
	}
}

// closingQuote is the index of the quote that closes the basic string that
// opens s, or -1.
func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		case '\n':
			return -1
		}
	}
	return -1
}

// decodeBasic decodes the inside of a basic string.
func decodeBasic(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("escape at the end of a string")
		}
		i++
		switch s[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '"', '\\':
			b.WriteByte(s[i])
		case 'u', 'U':
			n := 4
			if s[i] == 'U' {
				n = 8
			}
			if i+1+n > len(s) {
				return "", errors.New("short unicode escape")
			}
			v, err := strconv.ParseUint(s[i+1:i+1+n], 16, 32)
			if err != nil || !utf8.ValidRune(rune(v)) {
				return "", fmt.Errorf("bad unicode escape %q", s[i-1:i+1+n])
			}
			b.WriteRune(rune(v))
			i += n
		default:
			return "", fmt.Errorf("unknown escape \\%c", s[i])
		}
	}
	return b.String(), nil
}

// scanValue reads a value from pos to its end: strings, arrays and inline
// tables may span lines. It returns where the value text ends (before a
// comment) and where the item ends (past the line break).
func scanValue(doc string, pos int) (valEnd, end int, err error) {
	depth := 0
	valEnd = -1
	for i := pos; i < len(doc); i++ {
		switch c := doc[i]; {
		case strings.HasPrefix(doc[i:], `"""`) || strings.HasPrefix(doc[i:], `'''`):
			end := closeMultiline(doc, i+3, doc[i:i+3])
			if end < 0 {
				return 0, 0, errors.New("unterminated multi-line string")
			}
			i = end - 1
		case c == '"':
			k := closingQuote(doc[i:])
			if k < 0 {
				return 0, 0, errors.New("unterminated string")
			}
			i += k
		case c == '\'':
			k := strings.IndexAny(doc[i+1:], "'\n")
			if k < 0 || doc[i+1+k] != '\'' {
				return 0, 0, errors.New("unterminated string")
			}
			i += 1 + k
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == '#':
			if valEnd < 0 && depth == 0 {
				valEnd = i
			}
			for i < len(doc) && doc[i] != '\n' {
				i++
			}
			i--
		case c == '\n':
			if depth <= 0 {
				if valEnd < 0 {
					valEnd = i
				}
				return valEnd, i + 1, nil
			}
		}
	}
	if depth > 0 {
		return 0, 0, errors.New("unterminated array or inline table")
	}
	if valEnd < 0 {
		valEnd = len(doc)
	}
	return valEnd, len(doc), nil
}

// closeMultiline is the index after the delimiter q (""" or ”') that closes
// a multi-line string whose content starts at from, or -1. In a basic
// string, a quote after an odd run of backslashes is escaped; up to two
// quotes right before the closing belong to the content.
func closeMultiline(doc string, from int, q string) int {
	for k := from; ; {
		j := strings.Index(doc[k:], q)
		if j < 0 {
			return -1
		}
		pos := k + j
		if q[0] == '"' && pos > from && doc[pos-1] == '\\' && !evenBackslashes(doc, pos-1) {
			k = pos + 1
			continue
		}
		end := pos + 3
		for n := 0; n < 2 && end < len(doc) && doc[end] == q[0]; n++ {
			end++
		}
		return end
	}
}

// evenBackslashes reports whether the run of backslashes that ends at i has
// an even length (so the character after it is not escaped).
func evenBackslashes(doc string, i int) bool {
	n := 0
	for ; i >= 0 && doc[i] == '\\'; i-- {
		n++
	}
	return n%2 == 0
}

// setTOMLKey sets key = value (value already encoded) in the table of the
// given path ([] is the root), keeping everything else. An existing key
// with the same value changes nothing.
func setTOMLKey(doc []byte, table []string, key, value string) ([]byte, error) {
	text := string(doc)
	items, err := scanTOML(text)
	if err != nil {
		return nil, fmt.Errorf("config.toml: %w", err)
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	line := tomlKey(key) + " = " + value
	target := slices.Concat(table, []string{key})

	// The body of the table: the items after its header up to the next
	// header (for the root, the items before the first header).
	found, bodyStart, bodyEnd := len(table) == 0, 0, len(items)
	for i, it := range items {
		if it.kind != tomlHeader {
			continue
		}
		if found {
			bodyEnd = i
			break
		}
		if !it.array && slices.Equal(it.path, table) {
			found, bodyStart = true, i+1
		}
	}
	var current []string // the table of each item
	for i, it := range items {
		switch it.kind {
		case tomlHeader:
			current = it.path
			if it.array && hasPrefix(table, it.path) {
				return nil, fmt.Errorf("config.toml: %s is an array of tables; the Mestre does not edit it", strings.Join(it.path, "."))
			}
		case tomlKeyValue:
			full := slices.Concat(current, it.path)
			inBody := found && i >= bodyStart && i < bodyEnd
			switch {
			case inBody && slices.Equal(full, target):
				if it.value == value {
					return doc, nil
				}
				raw := text[it.start:it.end]
				indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
				ending := ""
				if strings.HasSuffix(raw, "\n") {
					ending = nl
				}
				return []byte(text[:it.start] + indent + line + ending + text[it.end:]), nil
			case hasPrefix(table, full), // a value where the table would be
				!inBody && len(current) < len(table) && hasPrefix(full, table): // the table defined by dotted keys
				return nil, fmt.Errorf("config.toml: %s is already defined in a form the Mestre does not edit", strings.Join(full, "."))
			}
		}
	}
	if found {
		// The new key goes right after the header, or after the last root
		// key, before the first table.
		at := len(text)
		switch {
		case len(table) > 0:
			at = items[bodyStart-1].end
		case bodyEnd < len(items):
			at = items[bodyEnd].start
		}
		prefix := text[:at]
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += nl
		}
		return []byte(prefix + line + nl + text[at:]), nil
	}
	head := make([]string, len(table))
	for i, s := range table {
		head[i] = tomlKey(s)
	}
	out := text
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += nl
	}
	if out != "" {
		out += nl
	}
	return []byte(out + "[" + strings.Join(head, ".") + "]" + nl + line + nl), nil
}

// hasPrefix reports whether p starts with prefix.
func hasPrefix(p, prefix []string) bool {
	return len(p) >= len(prefix) && slices.Equal(p[:len(prefix)], prefix)
}
