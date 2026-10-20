package quests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// criterionRe is an item `- [ ] C<n> <text>` of the body of an issue
// (docs/orquestracao/prova-e-devolucao.md#prova-por-critério).
var criterionRe = regexp.MustCompile(`^[ \t]*[-*] \[[ xX]\] (C\d+)[ \t]+(\S.*?)[ \t]*$`)

// parseCriteria reads the criteria of the body of an issue, in order; a
// repeated id keeps the first.
func parseCriteria(body string) []Criterion {
	var out []Criterion
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		m := criterionRe.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, Criterion{ID: m[1], Text: m[2], Pos: len(out)})
	}
	return out
}

// ProofInput is a `--proof "C<n>=<text>"` of a done.
type ProofInput struct{ Criterion, Text string }

// whereRe splits a place `path:line` or `path:line:column`.
var whereRe = regexp.MustCompile(`^(.*?):(\d+)(?::\d+)?$`)

// normalizeWhere is a place as the fingerprint reads it: a path relative to
// the worktree with `/`, without the column, and without a `./` in front.
func normalizeWhere(w, worktree string) string {
	w = strings.TrimSpace(strings.ReplaceAll(w, `\`, "/"))
	if m := whereRe.FindStringSubmatch(w); m != nil {
		w = m[1] + ":" + m[2]
	}
	if worktree != "" {
		wt := strings.TrimRight(strings.ReplaceAll(worktree, `\`, "/"), "/") + "/"
		w = strings.TrimPrefix(w, wt)
	}
	file, line, hasLine := strings.Cut(w, ":")
	file = path.Clean(file)
	if hasLine {
		return file + ":" + line
	}
	return file
}

// Fingerprint is the print of a failure: the first 12 hexadecimal digits of
// the SHA-256 of "step | cause | places", the places normalized, without
// repeats and in order. Without a place there is no print: "" and no places.
func Fingerprint(step, cause string, where []string, worktree string) (string, []string) {
	var places []string
	for _, w := range where {
		if n := normalizeWhere(w, worktree); n != "" && n != "." {
			places = append(places, n)
		}
	}
	slices.Sort(places)
	places = slices.Compact(places)
	if len(places) == 0 {
		return "", nil
	}
	sum := sha256.Sum256([]byte(step + " | " + cause + " | " + strings.Join(places, " | ")))
	return hex.EncodeToString(sum[:])[:12], places
}

// proofMark is the first characters of the line of proof the engine writes
// under a checked item.
const proofMark = "  - prova"

// neutral keeps the text of an agent from opening a comment that hides the
// markers of the engine, or from calling a person of GitHub.
var neutral = strings.NewReplacer("<!--", "<\u200b!--", "-->", "-\u200b->", "@", "@\u200b")

// proofLine is the line under a criterion that says how it was proved.
func proofLine(p Proof) string {
	who := "verificada pelo motor"
	if p.Origin != ProofVerified {
		who = "declarada por " + inbox.OneLine(p.By)
	}
	return fmt.Sprintf("%s (%s): %s", proofMark, who, neutral.Replace(inbox.OneLine(p.Text)))
}

// markCriteria brings the items of the criteria in the body of the issue to
// what the quest knows: an item with a proof of this round is checked and has
// its line of proof; one the engine checked in an earlier round and has no
// proof now is taken back; the others are left as they are (someone else may
// have checked them). It says which criteria the engine has checked now.
func markCriteria(body string, cs []Criterion, proofs []Proof) (string, map[string]bool) {
	byID := map[string]Proof{}
	for _, p := range proofs {
		byID[p.Criterion] = p
	}
	checked := map[string]bool{}
	seen := map[string]bool{}
	lines := strings.Split(body, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		m := criterionRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		var c Criterion
		found := false
		if m != nil {
			for _, x := range cs {
				if x.ID == m[1] && !seen[x.ID] {
					c, found = x, true
					seen[x.ID] = true
				}
			}
		}
		if !found {
			out = append(out, line)
			continue
		}
		// the proof line the engine wrote before goes; a new one replaces it
		skip := 0
		for i+1+skip < len(lines) && strings.HasPrefix(lines[i+1+skip], proofMark) {
			skip++
		}
		p, has := byID[c.ID]
		switch {
		case has && !c.Marked && strings.Contains(line, "[x]") || has && !c.Marked && strings.Contains(line, "[X]"):
			out = append(out, line) // somebody else checked it: it stays theirs
		case has:
			out = append(out, setBox(line, 'x'), proofLine(p))
			checked[c.ID] = true
			i += skip
		case c.Marked:
			out = append(out, setBox(line, ' '))
			i += skip
		default:
			out = append(out, line) // not ours to touch
		}
	}
	return strings.Join(out, "\n"), checked
}

// setBox changes the box of an item.
func setBox(line string, to rune) string {
	i := strings.Index(line, "[")
	if i < 0 || i+2 >= len(line) {
		return line
	}
	return line[:i+1] + string(to) + line[i+2:]
}

const (
	stepsBegin = "<!-- mestre:etapas -->"
	stepsEnd   = "<!-- /mestre:etapas -->"
)

// mergeSteps puts the checklist of the steps in the body of an issue the
// Master of the table wrote, between its markers; the rest of the body is
// not touched.
func mergeSteps(body, block string) string {
	wrapped := stepsBegin + "\n" + block + "\n" + stepsEnd
	if a := strings.Index(body, stepsBegin); a >= 0 {
		if b := strings.Index(body[a:], stepsEnd); b >= 0 {
			return body[:a] + wrapped + body[a+b+len(stepsEnd):]
		}
	}
	return strings.TrimRight(body, "\n") + "\n\n" + wrapped + "\n"
}

// freezeCriteria reads the criteria of the issue of the quest and keeps them
// for the round that starts. A quest with no issue, or whose issue has no
// item, runs without criteria.
func (s *Service) criteriaOf(ctx context.Context, q Quest) ([]Criterion, error) {
	if s.o.GitHub == nil || q.Def.Issue == 0 || q.Issue == 0 {
		return nil, nil
	}
	if s.o.Gate != nil {
		if err := s.o.Gate(); err != nil {
			return nil, err
		}
	}
	body, err := s.o.GitHub.IssueBody(ctx, q.Workspace, q.Issue)
	if err != nil {
		return nil, fmt.Errorf("the issue #%d could not be read for its criteria: %w", q.Issue, err)
	}
	return parseCriteria(body), nil
}
