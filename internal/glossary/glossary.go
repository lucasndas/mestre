// Package glossary reads the term table of docs/produto/glossario.md, the
// single source of the help texts of the panel and of Telegram. The same
// reader serves the documentation check (tools/docscheck) and, from M4,
// the generated help texts.
package glossary

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxHelp is the maximum length of the short help, in characters.
const MaxHelp = 120

// Term is one row of the table.
type Term struct {
	Name   string
	Help   string
	Detail string // relative link target of the owning document
	Line   int
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
			if len(cells) != 3 || cells[0] != "Termo" || cells[1] != "Ajuda curta" || cells[2] != "Detalhes" {
				problems = append(problems, Problem{n, "table header must be | Termo | Ajuda curta | Detalhes |"})
			}
			continue
		}
		if isSeparator(cells) {
			continue
		}
		if len(cells) != 3 {
			problems = append(problems, Problem{n, fmt.Sprintf("row has %d columns, want 3", len(cells))})
			continue
		}
		t := Term{Name: cells[0], Help: cells[1], Line: n}
		problems = append(problems, check(&t, cells[2])...)
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
	if strings.Contains(t.Help, "](") || strings.Contains(t.Help, "http://") || strings.Contains(t.Help, "https://") {
		ps = append(ps, Problem{t.Line, fmt.Sprintf("short help of %q has a link", t.Name)})
	}
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
