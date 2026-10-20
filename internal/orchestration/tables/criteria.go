package tables

import (
	"regexp"
	"strings"
)

// criteriaHeading is the heading of the section of the criteria in the body of
// an epic (docs/orquestracao/grao-mestre.md#triagem-e-roteamento-do-pedido).
var criteriaHeading = regexp.MustCompile(`^##\s+Crit[ée]rios\s*#*\s*$`)

// criterionItem is one item of the section: `- [ ] text` or `- [x] text`.
var criterionItem = regexp.MustCompile(`^\s*[-*]\s+\[[ xX]\]\s+(.+?)\s*$`)

// ParseCriteria reads the criteria the Grand Master wrote in the section
// `## Critérios` of the body of an epic, in order and without a number (the
// `C<n>` of the missions are numbered by the table master). The section ends at
// the next heading; text outside it, fenced code and empty items are not
// criteria. No section: none.
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
		if strings.HasPrefix(line, "#") {
			in = criteriaHeading.MatchString(trim)
			continue
		}
		if !in {
			continue
		}
		if m := criterionItem.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
