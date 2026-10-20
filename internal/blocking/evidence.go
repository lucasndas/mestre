package blocking

import (
	"regexp"
	"strings"
)

// placeLine is a line of the output of a test that says where and what: the
// `path:line` (and an optional `:column`), then the error. It is the format of
// `go test`, of the compilers and of most test runners.
var placeLine = regexp.MustCompile(`^\s*([^\s:][^\s]*?\.[A-Za-z0-9]+:\d+)(?::\d+)?:?\s+(.+?)\s*$`)

// maxEvidenceMatches is how many lines of the output the evidence quotes.
const maxEvidenceMatches = 2

// TestEvidence is the evidence the engine builds from a test that failed, not
// from what an agent says: `file:line → "error"` for the lines of the output
// that point at a place (the places the verification found come first), then
// the command. When no line points at a place, the last line of the output
// stands for the error. Nothing at all gives "" (the notice then says it has
// no evidence the engine could verify). Build cuts and masks it like any
// evidence.
func TestEvidence(command, output string, places []string) string {
	command = strings.TrimSpace(command)
	type hit struct{ where, msg string }
	var hits []hit
	var last string
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		last = strings.TrimSpace(line)
		if m := placeLine.FindStringSubmatch(strings.ReplaceAll(line, `\`, "/")); m != nil {
			hits = append(hits, hit{where: m[1], msg: m[2]})
		}
	}
	listed := func(h hit) bool {
		for _, p := range places {
			p = strings.ReplaceAll(p, `\`, "/")
			if strings.HasSuffix(p, h.where) || strings.HasSuffix(h.where, p) {
				return true
			}
		}
		return false
	}
	var out []string
	for pass := 0; pass < 2; pass++ {
		for _, h := range hits {
			if len(out) == maxEvidenceMatches {
				break
			}
			if listed(h) == (pass == 0) {
				out = append(out, h.where+` → "`+strings.ReplaceAll(h.msg, `"`, `'`)+`"`)
			}
		}
	}
	if len(out) == 0 && last != "" {
		out = append(out, `→ "`+strings.ReplaceAll(last, `"`, `'`)+`"`)
	}
	if len(out) > 0 && command != "" {
		out = append(out, "$ "+command)
	}
	return strings.Join(out, "\n")
}
