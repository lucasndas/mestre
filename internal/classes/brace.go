package classes

import (
	"regexp"
	"strconv"
	"strings"
)

// Brace expansion (bash, zsh, ksh): an unquoted {a,b} or {x..y} in a word
// makes several words, before any other expansion, so "{gi,x}t push" runs
// git push. The dispatcher expands it the same way and analyses every word
// it makes; the line is at least ask, since the text does not show them.

// maxBraceWords bounds the words one word expands to; more is denied.
const maxBraceWords = 256

// braceTooLarge is the reason of a line whose expansion passes the bound.
const braceTooLarge = "expansão de chaves grande demais para ser analisada"

type braceItem struct {
	c       byte
	special bool // an unquoted {, } or ,
}

// braceExpand expands the words of text, whose unquoted braces and commas
// are at the offsets marks. ok is false when it makes more than
// maxBraceWords words.
func braceExpand(text string, marks []int) (words []string, ok bool) {
	items := make([]braceItem, len(text))
	for i := range len(text) {
		items[i] = braceItem{c: text[i]}
	}
	for _, m := range marks {
		items[m].special = true
	}
	var out [][]braceItem
	if !expandBraces(items, &out) {
		return nil, false
	}
	for _, w := range out {
		b := make([]byte, len(w))
		for i, it := range w {
			b[i] = it.c
		}
		words = append(words, string(b))
	}
	return words, true
}

// expandBraces appends to out the words of items, expanding the first brace
// that is an expansion (a list with a comma at its level, or a sequence);
// a brace that is neither is literal, as in bash.
func expandBraces(items []braceItem, out *[][]braceItem) bool {
	if len(*out) > maxBraceWords {
		return false
	}
	for i, it := range items {
		if !it.special || it.c != '{' {
			continue
		}
		depth, end := 0, -1
		var commas []int
	scan:
		for k := i + 1; k < len(items); k++ {
			if !items[k].special {
				continue
			}
			switch items[k].c {
			case '{':
				depth++
			case '}':
				if depth == 0 {
					end = k
					break scan
				}
				depth--
			case ',':
				if depth == 0 {
					commas = append(commas, k)
				}
			}
		}
		if end < 0 {
			continue
		}
		var alts [][]braceItem
		if len(commas) > 0 {
			start := i + 1
			for _, c := range append(commas, end) {
				alts = append(alts, items[start:c])
				start = c + 1
			}
		} else {
			seq, isSeq, fits := braceSequence(items[i+1 : end])
			if !isSeq {
				continue
			}
			if !fits {
				return false
			}
			for _, s := range seq {
				var alt []braceItem
				for j := range len(s) {
					alt = append(alt, braceItem{c: s[j]})
				}
				alts = append(alts, alt)
			}
		}
		for _, a := range alts {
			w := make([]braceItem, 0, len(items))
			w = append(append(append(w, items[:i]...), a...), items[end+1:]...)
			if !expandBraces(w, out) {
				return false
			}
		}
		return true
	}
	*out = append(*out, items)
	return len(*out) <= maxBraceWords
}

var (
	numSeqRe  = regexp.MustCompile(`^(-?[0-9]+)\.\.(-?[0-9]+)(?:\.\.(-?[0-9]+))?$`)
	charSeqRe = regexp.MustCompile(`^([A-Za-z])\.\.([A-Za-z])(?:\.\.(-?[0-9]+))?$`)
)

// braceSequence is the words of a sequence {x..y[..step]}: isSeq is false
// when the text is not one, fits false when it makes too many words.
func braceSequence(inner []braceItem) (seq []string, isSeq, fits bool) {
	b := make([]byte, len(inner))
	for i, it := range inner {
		if it.special {
			return nil, false, false
		}
		b[i] = it.c
	}
	s := string(b)
	step := func(t string) (int, bool) {
		if t == "" {
			return 1, true
		}
		n, err := strconv.Atoi(t)
		if err != nil || n < -maxBraceWords*1000 || n > maxBraceWords*1000 {
			return 0, false
		}
		if n < 0 {
			n = -n
		}
		return max(n, 1), true
	}
	if m := charSeqRe.FindStringSubmatch(s); m != nil {
		st, ok := step(m[3])
		if !ok {
			return nil, true, false
		}
		return ranged(int(m[1][0]), int(m[2][0]), st, func(n int) string { return string(rune(n)) })
	}
	m := numSeqRe.FindStringSubmatch(s)
	if m == nil {
		return nil, false, false
	}
	from, err1 := strconv.Atoi(m[1])
	to, err2 := strconv.Atoi(m[2])
	st, ok := step(m[3])
	const bound = 1 << 40
	if err1 != nil || err2 != nil || !ok || from < -bound || from > bound || to < -bound || to > bound {
		return nil, true, false
	}
	width := 0
	for _, t := range []string{m[1], m[2]} {
		if d := strings.TrimPrefix(t, "-"); len(d) > 1 && d[0] == '0' {
			width = max(width, len(t))
		}
	}
	return ranged(from, to, st, func(n int) string {
		t := strconv.Itoa(n)
		if neg := strings.HasPrefix(t, "-"); len(t) < width {
			digits := strings.TrimPrefix(t, "-")
			pad := strings.Repeat("0", width-len(t))
			if neg {
				return "-" + pad + digits
			}
			return pad + digits
		}
		return t
	})
}

// ranged is from to to by step, in either direction.
func ranged(from, to, step int, text func(int) string) ([]string, bool, bool) {
	span := to - from
	if span < 0 {
		span = -span
	}
	if span/step+1 > maxBraceWords {
		return nil, true, false
	}
	var out []string
	for n := from; ; {
		out = append(out, text(n))
		if (from <= to && n+step > to) || (from > to && n-step < to) {
			break
		}
		if from <= to {
			n += step
		} else {
			n -= step
		}
	}
	return out, true, true
}
