package main

import (
	"regexp"
	"strings"
)

// The one exception mechanism of the rules of this tool:
//
//	// mestre:allow <rule>: <reason>
//
// on the line of the violation or the line above. The reason is required, the
// rule must exist, and an exception that excuses nothing is an error, so the
// exceptions never outlive the code they were written for.
var allowRe = regexp.MustCompile(`^mestre:allow\s+([a-z-]+)\s*(?::\s*(.*))?$`)

var knownRules = map[string]bool{
	ruleParallel: true, ruleSelect: true, ruleStdio: true,
	ruleNilSA: true, rulePrivateDir: true, ruleSecret: true,
}

type allowance struct {
	line int
	rule string
	used bool
}

// applyAllows drops the issues excused by a mestre:allow comment of the file
// and adds the issues of the comments themselves.
func applyAllows(s *source, issues []Issue) []Issue {
	var allows []*allowance
	var out []Issue
	for _, cg := range s.file.Comments {
		for _, c := range cg.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if !strings.HasPrefix(text, "mestre:allow") || !strings.HasPrefix(c.Text, "//") {
				continue
			}
			line := s.line(c.Pos())
			m := allowRe.FindStringSubmatch(text)
			switch {
			case m == nil:
				out = append(out, Issue{s.path, line, "allow", "write `// mestre:allow <rule>: <reason>`"})
			case !knownRules[m[1]]:
				out = append(out, Issue{s.path, line, "allow", "unknown rule " + m[1]})
			case strings.TrimSpace(m[2]) == "":
				out = append(out, Issue{s.path, line, "allow", "mestre:allow " + m[1] + " needs a reason after the colon"})
			default:
				allows = append(allows, &allowance{line: line, rule: m[1]})
			}
		}
	}
	for _, is := range issues {
		excused := false
		for _, a := range allows {
			if a.rule == is.Rule && (a.line == is.Line || a.line == is.Line-1) {
				a.used, excused = true, true
			}
		}
		if !excused {
			out = append(out, is)
		}
	}
	for _, a := range allows {
		if !a.used {
			out = append(out, Issue{s.path, a.line, "allow", "mestre:allow " + a.rule + " excuses nothing: remove it"})
		}
	}
	return out
}
