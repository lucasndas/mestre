package telegram

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/glossary"
)

// commonWords are the one-word terms of the glossary that are also a verb or a
// common word: they count only with a capital letter, away from the start of a
// sentence (docs/telegram/bot.md#explicação-de-termos-na-primeira-vez).
var commonWords = map[string]bool{
	"Aceite": true, "Vigia": true, "Prova": true, "Espera": true, "Base": true, "Ficha": true, "Etapa": true, "Receita": true,
	"Risco": true, "Nível": true, "Superior": true, "Impressão": true, "Fixado": true, "Triagem": true, "Mesa": true, "Classe": true,
	"Torre": true, "Época": true, "Espelho": true, "Ritual": true, "Terminal": true, "Missão": true, "Decreto": true, "Devolução": true,
}

type termMatch struct {
	start, end int // bytes
	term       glossary.Term
}

// explain finds the terms of the glossary in a text that the user has not seen
// yet and puts the short help in parentheses right after the first use of each
// (the longest term wins: "Mestre da mesa" is explained, not "Mestre"). The
// code (fences and spans) is left alone. It returns the new text and the terms
// it explained.
func explain(text string, terms []glossary.Term, seen map[string]bool) (string, []string) {
	skip := codeRanges(text)
	lower := strings.ToLower(text)
	var found []termMatch
	for _, t := range terms {
		if seen[t.Name] {
			continue
		}
		needle := strings.ToLower(t.Name)
		for from := 0; ; {
			i := strings.Index(lower[from:], needle)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(needle)
			from = end
			if start+len(needle) > len(text) || inRanges(skip, start) || !wordEdge(text, start, end) {
				continue
			}
			if commonWords[t.Name] && !strings.Contains(t.Name, " ") && !properUse(text, start) {
				continue
			}
			found = append(found, termMatch{start, end, t})
			break // the first use is enough
		}
	}
	// The longest match wins where two overlap.
	sort.SliceStable(found, func(i, j int) bool { return found[i].end-found[i].start > found[j].end-found[j].start })
	var kept []termMatch
	for _, m := range found {
		overlap := false
		for _, k := range kept {
			if m.start < k.end && k.start < m.end {
				overlap = true
				break
			}
		}
		if !overlap {
			kept = append(kept, m)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].start > kept[j].start })
	var names []string
	for _, m := range kept {
		text = text[:m.end] + " (" + m.term.Help + ")" + text[m.end:]
		names = append(names, m.term.Name)
	}
	return text, names
}

// codeRanges are the byte ranges of fences and code spans.
func codeRanges(s string) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "```"):
			j := strings.Index(s[i+3:], "```")
			if j < 0 {
				out = append(out, [2]int{i, len(s)})
				return out
			}
			out = append(out, [2]int{i, i + 3 + j + 3})
			i += 3 + j + 3
		case s[i] == '`':
			j := strings.IndexByte(s[i+1:], '`')
			if j < 0 {
				out = append(out, [2]int{i, len(s)})
				return out
			}
			out = append(out, [2]int{i, i + 1 + j + 1})
			i += 1 + j + 1
		default:
			i++
		}
	}
	return out
}

func inRanges(r [][2]int, at int) bool {
	for _, x := range r {
		if at >= x[0] && at < x[1] {
			return true
		}
	}
	return false
}

// wordEdge says the match [start,end) is a whole word (or phrase).
func wordEdge(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if isWord(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if isWord(r) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' }

// properUse says a common word starts with a capital letter and is not at the
// start of a sentence.
func properUse(s string, at int) bool {
	r, _ := utf8.DecodeRuneInString(s[at:])
	if !unicode.IsUpper(r) {
		return false
	}
	before := strings.TrimRight(s[:at], " \t")
	if before == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(before)
	return last != '.' && last != '!' && last != '?' && last != '\n'
}
