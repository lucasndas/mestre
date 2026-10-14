package classes

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Brace expansion (bash, zsh, ksh): an unquoted {a,b} or {x..y} in a word
// makes several words, before any other expansion, so "{gi,x}t push" runs
// git push. The dispatcher expands it the same way and analyses every word
// it makes; the line is at least ask, since the text does not show them.

// maxBraceWords bounds the words one word expands to; more is denied.
const maxBraceWords = 256

// maxBraceMarks bounds the unquoted braces and commas of one word that has
// a brace; maxBraceWork bounds the bytes the expansion of one word goes
// through. Past either, the word is too large to analyse and the line is
// denied: the scan is linear in the word and the expansion is bounded.
const (
	maxBraceMarks = 4096
	maxBraceWork  = 1 << 20
)

// braceTooLarge is the reason of a line whose expansion passes the bounds.
const braceTooLarge = "expansão de chaves grande demais para ser analisada"

type braceItem struct {
	c       byte
	special bool // an unquoted {, } or ,
	quoted  bool // from a quoted part of the word
	gap     bool // an empty quoted part ("" or ''): no byte, but quoted
}

// braceWord is one word an expansion makes; quoted is true when a quoted
// part of the word (even an empty one) went into it: the shell keeps such
// a word when it is empty, and drops an empty one that is not.
type braceWord struct {
	text   string
	quoted bool
}

// quoteSpan is a part of a word, by offsets of its text, that was quoted.
type quoteSpan struct{ from, to int }

// braceExpand expands the words of text, whose unquoted braces and commas
// are at the offsets marks (in order) and whose quoted parts are spans. ok
// is false when it passes the bounds (maxBraceMarks, maxBraceWords,
// maxBraceWork).
func braceExpand(text string, marks []int, spans []quoteSpan) (words []braceWord, ok bool) {
	if len(marks) > maxBraceMarks {
		return nil, false
	}
	gaps := map[int]int{}
	quoted := make([]bool, len(text))
	for _, s := range spans {
		if s.from == s.to {
			gaps[s.from]++
		}
		for i := s.from; i < s.to && i < len(text); i++ {
			quoted[i] = true
		}
	}
	special := make([]bool, len(text))
	for _, m := range marks {
		special[m] = true
	}
	items := make([]braceItem, 0, len(text)+len(gaps))
	for i := range len(text) + 1 {
		for range gaps[i] {
			items = append(items, braceItem{gap: true, quoted: true})
		}
		if i < len(text) {
			items = append(items, braceItem{c: text[i], special: special[i], quoted: quoted[i]})
		}
	}
	x := &braceExpander{}
	if !x.expand(items) {
		return nil, false
	}
	for _, w := range x.out {
		b := make([]byte, 0, len(w))
		q := false
		for _, it := range w {
			q = q || it.quoted
			if !it.gap {
				b = append(b, it.c)
			}
		}
		words = append(words, braceWord{text: string(b), quoted: q})
	}
	return words, true
}

// braceExpander holds the words made so far and the work done.
type braceExpander struct {
	out  [][]braceItem
	work int
}

// bracePair is a brace with its match: the offsets of the {, of the } and
// of the commas at its level.
type bracePair struct {
	open, close int
	commas      []int
}

// bracePairs matches the special braces of items in one pass, with a
// stack, sorted by their opening brace. An unmatched { is literal, as in
// bash.
func bracePairs(items []braceItem) []bracePair {
	var stack, pairs []bracePair
	for k, it := range items {
		if !it.special {
			continue
		}
		switch it.c {
		case '{':
			stack = append(stack, bracePair{open: k})
		case ',':
			if n := len(stack); n > 0 {
				stack[n-1].commas = append(stack[n-1].commas, k)
			}
		case '}':
			if n := len(stack); n > 0 {
				p := stack[n-1]
				stack = stack[:n-1]
				p.close = k
				pairs = append(pairs, p)
			}
		}
	}
	slices.SortFunc(pairs, func(a, b bracePair) int { return a.open - b.open })
	return pairs
}

// expand appends to out the words of items, expanding the first brace that
// is an expansion (a list with a comma at its level, or a sequence); a
// brace that is neither is literal, as in bash.
func (x *braceExpander) expand(items []braceItem) bool {
	x.work += len(items) + 1
	if x.work > maxBraceWork || len(x.out) > maxBraceWords {
		return false
	}
	for _, p := range bracePairs(items) {
		i, end := p.open, p.close
		var alts [][]braceItem
		if len(p.commas) > 0 {
			start := i + 1
			for _, c := range append(p.commas, end) {
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
				alt := make([]braceItem, len(s))
				for j := range len(s) {
					alt[j] = braceItem{c: s[j]}
				}
				alts = append(alts, alt)
			}
		}
		for _, a := range alts {
			w := make([]braceItem, 0, len(items))
			w = append(append(append(w, items[:i]...), a...), items[end+1:]...)
			if !x.expand(w) {
				return false
			}
		}
		return true
	}
	x.out = append(x.out, items)
	return len(x.out) <= maxBraceWords
}

var (
	numSeqRe  = regexp.MustCompile(`^(-?[0-9]+)\.\.(-?[0-9]+)(?:\.\.(-?[0-9]+))?$`)
	charSeqRe = regexp.MustCompile(`^([A-Za-z])\.\.([A-Za-z])(?:\.\.(-?[0-9]+))?$`)
)

// braceSequence is the words of a sequence {x..y[..step]}: isSeq is false
// when the text is not one (a quoted part makes it literal, as in bash),
// fits false when it makes too many words.
func braceSequence(inner []braceItem) (seq []string, isSeq, fits bool) {
	b := make([]byte, 0, len(inner))
	for _, it := range inner {
		if it.special || it.quoted {
			return nil, false, false
		}
		b = append(b, it.c)
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
