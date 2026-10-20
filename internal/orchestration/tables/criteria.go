package tables

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// criteriaHeading is the heading of the section of the criteria in the body of
// an epic (docs/orquestracao/grao-mestre.md#triagem-e-roteamento-do-pedido).
var criteriaHeading = regexp.MustCompile(`(?i)^ {0,3}##\s+Crit[ée]rios\s*:?\s*#*\s*$`)

// anyHeading is a Markdown heading: up to three spaces, one to six marks and a
// space (a line such as `#42 depends on this` is not one).
var anyHeading = regexp.MustCompile(`^ {0,3}#{1,6}(\s|$)`)

// criterionItem is one item of the section: `- [ ] text` or `- [x] text`.
var criterionItem = regexp.MustCompile(`^\s*[-*]\s+\[[ xX]\]\s+(\S.*?)\s*$`)

// htmlComment is what a Markdown reader does not show.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// The text of an epic is not trusted: it goes to the context of the Grand
// Master, so how much of it does is limited, and what a person cannot see in
// it (control and format characters: bidirectional marks, zero width, tags) is
// taken out.
const (
	// MaxCriteria is the most criteria an epic may have for the engine to read
	// them; more is an error, never a silent cut.
	MaxCriteria      = 50
	maxCriterionRune = 500
)

// ErrCriteria is why the criteria of an epic cannot be read as they are.
var ErrCriteria = errors.New("the criteria of the epic cannot be read")

// ParseCriteria reads the criteria the Grand Master wrote in the section
// `## Critérios` of the body of an epic, in order and without a number (the
// `C<n>` of the missions are numbered by the table master). The section ends at
// the next heading; text outside it, comments, fenced code and empty items are
// not criteria. No section: none. Two sections, a fence that is not closed or
// more than MaxCriteria items are an error (ErrCriteria): the epic is
// ambiguous, and an empty list would be taken for an epic without criteria.
func ParseCriteria(body string) ([]string, error) {
	out := []string{}
	body = htmlComment.ReplaceAllString(strings.ReplaceAll(body, "\r\n", "\n"), "")
	in, fenced, sections := false, false, 0
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if anyHeading.MatchString(line) {
			in = criteriaHeading.MatchString(line)
			if in {
				if sections++; sections > 1 {
					return []string{}, fmt.Errorf("%w: the epic has more than one section `## Critérios`", ErrCriteria)
				}
			}
			continue
		}
		if !in {
			continue
		}
		if m := criterionItem.FindStringSubmatch(line); m != nil {
			if c := CleanText(m[1]); c != "" {
				if len(out) == MaxCriteria {
					return []string{}, fmt.Errorf("%w: the epic has more than %d criteria", ErrCriteria, MaxCriteria)
				}
				out = append(out, c)
			}
		}
	}
	if fenced {
		return []string{}, fmt.Errorf("%w: a block of code is not closed", ErrCriteria)
	}
	return out, nil
}

// CleanText takes the control and the format characters out of a text that
// somebody else wrote and cuts it to the size of a criterion.
func CleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxCriterionRune {
		s = string(r[:maxCriterionRune]) + "…"
	}
	return s
}
