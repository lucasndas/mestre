package store

import "strings"

// A small reading of SQL text, enough for the guards of the writers: where a
// script splits into statements and what each one starts with. It is not a
// parser, and nothing depends on it alone: what it misses is caught at commit
// (the schema version, the commit hook).

// splitStatements returns the statements of a script, in order: the text
// between the semicolons that are not inside a string, a quoted name or a
// comment. Empty statements (";;", a trailing ";") and comments between them
// are dropped. The body of a trigger has semicolons of its own and would
// come out as several statements; triggers are DDL, which only the
// migrations run and which they run as scripts, never through this split.
func splitStatements(q string) []string {
	var out []string
	start := 0
	flush := func(end int) {
		if s := strings.TrimSpace(skipBlank(q[start:end])); s != "" {
			out = append(out, s)
		}
	}
	for i := 0; i < len(q); i++ {
		switch c := q[i]; {
		case c == ';':
			flush(i)
			start = i + 1
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(q, i, c)
		case c == '[':
			i = skipQuoted(q, i, ']')
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			i = indexFrom(q, i, "\n")
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			i = indexFrom(q, i+2, "*/")
		}
	}
	flush(len(q))
	return out
}

// skipQuoted returns the index of the byte that closes the quote opened at i
// (a doubled quote inside is part of the text), or the end of q.
func skipQuoted(q string, i int, closer byte) int {
	for j := i + 1; j < len(q); j++ {
		if q[j] != closer {
			continue
		}
		if closer != ']' && j+1 < len(q) && q[j+1] == closer {
			j++
			continue
		}
		return j
	}
	return len(q)
}

// indexFrom is the index of the first sep at or after from, or the last byte
// of q when there is none.
func indexFrom(q string, from int, sep string) int {
	if from >= len(q) {
		return len(q) - 1
	}
	if k := strings.Index(q[from:], sep); k >= 0 {
		return from + k + len(sep) - 1
	}
	return len(q) - 1
}

// firstWord is the leading keyword of a statement, in upper case.
func firstWord(s string) (word, rest string) {
	s = skipBlank(s)
	end := 0
	for end < len(s) && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= 'A' && s[end] <= 'Z') {
		end++
	}
	return strings.ToUpper(s[:end]), s[end:]
}

// pragmaOf reads a PRAGMA statement: its name (lower case, without the
// schema) and whether it sets a value, either as `name = v` or as `name(v)`.
// A few pragmas take an argument and only read (table_info(t)); the caller
// knows which.
func pragmaOf(rest string) (name string, sets bool) {
	rest = strings.TrimSpace(skipBlank(rest))
	end := 0
	for end < len(rest) && (rest[end] == '_' || rest[end] == '.' || rest[end] >= 'a' && rest[end] <= 'z' || rest[end] >= 'A' && rest[end] <= 'Z' || rest[end] >= '0' && rest[end] <= '9') {
		end++
	}
	name = strings.ToLower(rest[:end])
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	tail := strings.TrimSpace(skipBlank(rest[end:]))
	return name, strings.HasPrefix(tail, "=") || strings.HasPrefix(tail, "(")
}
