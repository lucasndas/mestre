// Package glossary reads the term table of docs/produto/glossario.md, the
// single source of the help texts of the panel and of Telegram. The same
// reader serves the documentation check (tools/docscheck) and, from M4,
// the generated help texts.
package glossary

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The limits of the texts, in characters.
const (
	// MaxHelp is the maximum length of the short help.
	MaxHelp = 120
	// MaxReal is the maximum length of the real-world phrase.
	MaxReal = 80
)

// Term is one row of the table, with its English version.
type Term struct {
	Name string
	Help string
	// Real says the term in everyday words, for those who do not know RPG.
	Real   string
	Detail string // relative link target of the owning document
	Line   int
	// En is the English version (filled by Combine).
	En English
}

// English is one row of the English table: the texts of a Term in English.
type English struct {
	Key  string // the Portuguese name of the term, as in the main table
	Name string
	Help string
	Real string
	Line int
}

// Problem is a format error at a line of the glossary.
type Problem struct {
	Line int
	Msg  string
}

var (
	linkRe   = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// Parse reads the table under "## Termos". It returns the terms and every
// format problem found.
func Parse(r io.Reader) ([]Term, []Problem) {
	var (
		terms    []Term
		problems []Problem
		inTerms  bool
		header   bool
		seen     = map[string]int{}
		n        int
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## ") {
			inTerms = line == "## Termos"
			continue
		}
		if !inTerms || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if !header {
			header = true
			if len(cells) != 4 || cells[0] != "Termo" || cells[1] != "Ajuda curta" || cells[2] != "No mundo real" || cells[3] != "Detalhes" {
				problems = append(problems, Problem{n, "table header must be | Termo | Ajuda curta | No mundo real | Detalhes |"})
			}
			continue
		}
		if isSeparator(cells) {
			continue
		}
		if len(cells) != 4 {
			problems = append(problems, Problem{n, fmt.Sprintf("row has %d columns, want 4", len(cells))})
			continue
		}
		t := Term{Name: cells[0], Help: cells[1], Real: cells[2], Line: n}
		problems = append(problems, check(&t, cells[3])...)
		if prev, ok := seen[t.Name]; ok {
			problems = append(problems, Problem{n, fmt.Sprintf("term %q repeated (first at line %d)", t.Name, prev)})
		}
		seen[t.Name] = n
		terms = append(terms, t)
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, Problem{n, err.Error()})
	}
	if !header {
		problems = append(problems, Problem{1, `no term table under "## Termos"`})
	}
	return terms, problems
}

func check(t *Term, detail string) []Problem {
	var ps []Problem
	if first, _ := utf8.DecodeRuneInString(t.Name); t.Name == "" || !unicode.IsUpper(first) {
		ps = append(ps, Problem{t.Line, fmt.Sprintf("term %q must start with a capital letter", t.Name)})
	}
	switch l := utf8.RuneCountInString(t.Help); {
	case l == 0:
		ps = append(ps, Problem{t.Line, fmt.Sprintf("term %q has no short help", t.Name)})
	case l > MaxHelp:
		ps = append(ps, Problem{t.Line, fmt.Sprintf("short help of %q has %d characters, max %d", t.Name, l, MaxHelp)})
	}
	if hasLink(t.Help) {
		ps = append(ps, Problem{t.Line, fmt.Sprintf("short help of %q has a link", t.Name)})
	}
	ps = append(ps, checkReal(t.Name, t.Real, t.Line)...)
	m := linkRe.FindAllStringSubmatch(detail, -1)
	if len(m) != 1 || strings.TrimSpace(linkRe.ReplaceAllString(detail, "")) != "" {
		ps = append(ps, Problem{t.Line, fmt.Sprintf("details of %q must be exactly one link", t.Name)})
		return ps
	}
	t.Detail = m[0][1]
	if schemeRe.MatchString(t.Detail) || strings.HasPrefix(t.Detail, "/") {
		ps = append(ps, Problem{t.Line, fmt.Sprintf("details of %q must be a relative link", t.Name)})
	}
	return ps
}

func splitRow(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func hasLink(s string) bool {
	return strings.Contains(s, "](") || strings.Contains(s, "http://") || strings.Contains(s, "https://")
}

func checkReal(name, real string, line int) []Problem {
	switch l := utf8.RuneCountInString(real); {
	case l == 0:
		return []Problem{{line, fmt.Sprintf("term %q has no real-world phrase", name)}}
	case l > MaxReal:
		return []Problem{{line, fmt.Sprintf("real-world phrase of %q has %d characters, max %d", name, l, MaxReal)}}
	}
	if hasLink(real) {
		return []Problem{{line, fmt.Sprintf("real-world phrase of %q has a link", name)}}
	}
	return nil
}

// ParseEnglish reads the table under "## Terms" of the English companion
// (glossario-en.md): | Termo | Term | Short help | In the real world |.
func ParseEnglish(r io.Reader) ([]English, []Problem) {
	var (
		rows     []English
		problems []Problem
		inTerms  bool
		header   bool
		seen     = map[string]int{}
		n        int
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## ") {
			inTerms = line == "## Terms"
			continue
		}
		if !inTerms || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if !header {
			header = true
			if len(cells) != 4 || cells[0] != "Termo" || cells[1] != "Term" || cells[2] != "Short help" || cells[3] != "In the real world" {
				problems = append(problems, Problem{n, "table header must be | Termo | Term | Short help | In the real world |"})
			}
			continue
		}
		if isSeparator(cells) {
			continue
		}
		if len(cells) != 4 {
			problems = append(problems, Problem{n, fmt.Sprintf("row has %d columns, want 4", len(cells))})
			continue
		}
		e := English{Key: cells[0], Name: cells[1], Help: cells[2], Real: cells[3], Line: n}
		if e.Name == "" {
			problems = append(problems, Problem{n, fmt.Sprintf("term %q has no English name", e.Key)})
		}
		switch l := utf8.RuneCountInString(e.Help); {
		case l == 0:
			problems = append(problems, Problem{n, fmt.Sprintf("term %q has no English short help", e.Key)})
		case l > MaxHelp:
			problems = append(problems, Problem{n, fmt.Sprintf("English short help of %q has %d characters, max %d", e.Key, l, MaxHelp)})
		}
		if hasLink(e.Help) {
			problems = append(problems, Problem{n, fmt.Sprintf("English short help of %q has a link", e.Key)})
		}
		problems = append(problems, checkReal(e.Key, e.Real, n)...)
		if prev, ok := seen[e.Key]; ok {
			problems = append(problems, Problem{n, fmt.Sprintf("term %q repeated (first at line %d)", e.Key, prev)})
		}
		seen[e.Key] = n
		rows = append(rows, e)
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, Problem{n, err.Error()})
	}
	if !header {
		problems = append(problems, Problem{1, `no term table under "## Terms"`})
	}
	return rows, problems
}

// Combine puts the English rows on the terms. Every term needs its row and
// every row a term.
func Combine(terms []Term, en []English) ([]Term, []Problem) {
	var problems []Problem
	by := map[string]English{}
	for _, e := range en {
		by[e.Key] = e
	}
	have := map[string]bool{}
	out := make([]Term, len(terms))
	for i, t := range terms {
		have[t.Name] = true
		e, ok := by[t.Name]
		if !ok {
			problems = append(problems, Problem{t.Line, fmt.Sprintf("term %q has no row in the English table", t.Name)})
		}
		t.En = e
		out[i] = t
	}
	for _, e := range en {
		if !have[e.Key] {
			problems = append(problems, Problem{e.Line, fmt.Sprintf("English row %q has no term in the glossary", e.Key)})
		}
	}
	return out, problems
}

// Load reads and checks both tables and returns the terms with their English
// versions; it fails with every problem found, each with file and line.
func Load(path, pathEN string) ([]Term, error) {
	var msgs []string
	read := func(path string, parse func(io.Reader) []Problem) {
		f, err := os.Open(path)
		if err != nil {
			msgs = append(msgs, err.Error())
			return
		}
		defer f.Close()
		for _, p := range parse(f) {
			msgs = append(msgs, fmt.Sprintf("%s:%d: %s", path, p.Line, p.Msg))
		}
	}
	var (
		terms []Term
		en    []English
	)
	read(path, func(r io.Reader) []Problem { var ps []Problem; terms, ps = Parse(r); return ps })
	read(pathEN, func(r io.Reader) []Problem { var ps []Problem; en, ps = ParseEnglish(r); return ps })
	if len(msgs) == 0 {
		var ps []Problem
		terms, ps = Combine(terms, en)
		for _, p := range ps {
			msgs = append(msgs, fmt.Sprintf("%s:%d: %s", path, p.Line, p.Msg))
		}
	}
	if len(msgs) > 0 {
		return nil, fmt.Errorf("%d problem(s) in the glossary:\n%s", len(msgs), strings.Join(msgs, "\n"))
	}
	return terms, nil
}
