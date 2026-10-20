package tables

import (
	"regexp"
	"strings"
	"unicode"
)

// criteriaHeading is the heading of the section of the criteria in the body of
// an epic (docs/orquestracao/grao-mestre.md#triagem-e-roteamento-do-pedido).
var criteriaHeading = regexp.MustCompile(`(?i)^##\s+Crit[ée]rios\s*#*\s*$`)

// anyHeading is a Markdown heading: one to six marks and a space (a line such
// as `#42 depends on this` is not one).
var anyHeading = regexp.MustCompile(`^#{1,6}(\s|$)`)

// criterionItem is one item of the section: `- [ ] text` or `- [x] text`.
var criterionItem = regexp.MustCompile(`^\s*[-*]\s+\[[ xX]\]\s+(\S.*?)\s*$`)

// The text of an epic is not trusted: it goes to the context of the Grand
// Master, so how much of it does is limited.
const (
	maxCriteria      = 50
	maxCriterionRune = 500
)

// ParseCriteria reads the criteria the Grand Master wrote in the section
// `## Critérios` of the body of an epic, in order and without a number (the
// `C<n>` of the missions are numbered by the table master). The section ends at
// the next heading; text outside it, fenced code and empty items are not
// criteria. At most 50 criteria of 500 characters, without control characters.
// No section: none.
func ParseCriteria(body string) []string {
	out := []string{}
	in, fenced := false, false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if anyHeading.MatchString(line) {
			in = criteriaHeading.MatchString(trim)
			continue
		}
		if !in {
			continue
		}
		if m := criterionItem.FindStringSubmatch(line); m != nil {
			if len(out) == maxCriteria {
				break
			}
			out = append(out, clean(m[1]))
		}
	}
	return out
}

// clean takes the control characters out of a text and cuts it to the limit.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxCriterionRune {
		s = string(r[:maxCriterionRune]) + "…"
	}
	return s
}
