package chain

import (
	"context"
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
)

// HumanLabel is the label that keeps a PR from the Grão-Mestre
// (docs/aprovacoes/politica.md#o-que-o-motor-lê-do-pr).
const HumanLabel = "mestre:humano"

// ReasonIncompleteDiff is the reason of a decision the engine could not check
// because it did not read the whole PR.
const ReasonIncompleteDiff = "diff_incompleto"

// PRRef is the PR (and the commit) a request of type pr or merge is about.
type PRRef struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	SHA    string `json:"sha"`
}

// PRFile is a file of the diff of a PR at the hash of the request.
type PRFile struct {
	Path string
	// PreviousPath is where a renamed file was: the areas look at both.
	PreviousPath string
	Added        int
	Removed      int
	Deleted      bool
	// PatchMissing says the file is text and GitHub gave no patch for it (too
	// big): the engine cannot read it.
	PatchMissing bool
	// AddedLines are the lines the PR adds, without the `+`.
	AddedLines []string
}

// PRFacts is what the engine read of a PR on GitHub, at the hash of the
// request: nothing here comes from an agent.
type PRFacts struct {
	Files []PRFile
	// Incomplete says the list of files was cut by GitHub.
	Incomplete bool
	Labels     []string
	// LabelsRead says the labels were read; false makes the decision human.
	LabelsRead bool
	// CIGreen says the checks of the hash are green; ReviewDone that another
	// character finished a review with nothing pending.
	CIGreen, ReviewDone bool
	// OpenedByGrandMaster says the PR was opened by the App "Grão-Mestre": the
	// same App can not approve it, so only a person does
	// (docs/github/aprovacao-no-github.md#quem-abre-e-quem-aprova).
	OpenedByGrandMaster bool
}

// PRSource reads a PR for the conditions of the policy. An error is a
// decision for a person (diff_incompleto): the engine never fails down.
type PRSource interface {
	PullRequest(ctx context.Context, workspace string, ref PRRef) (PRFacts, error)
}

// RulesSource gives the rules of the policy file of a workspace and its
// version (hash). The PolicyFile is one.
type RulesSource interface {
	Rules(workspace string) (PolicyRules, string)
}

// Rules implements RulesSource: the rules that hold (the last valid ones).
func (f *PolicyFile) Rules(workspace string) (PolicyRules, string) {
	p := f.load(workspace)
	if p.rules == nil {
		return DefaultRules(), p.version
	}
	return *p.rules, p.version
}

// rulesOf is the rules of a workspace: the default content when the policy
// gives none.
func (s *Service) rulesOf(workspace string) (PolicyRules, string) {
	if rs, ok := s.o.Policy.(RulesSource); ok {
		return rs.Rules(workspace)
	}
	return DefaultRules(), "default"
}

// ignored says whether the file counts neither in the lines nor in the words.
func (r PolicyRules) ignored(path string) bool {
	for _, g := range r.IgnoredLines {
		if classes.MatchGlob(g, path) {
			return true
		}
	}
	return false
}

// Conditions are the reasons the Grão-Mestre cannot decide the request alone,
// from the conditions the engine checks (docs/aprovacoes/politica.md#quem-confere-o-quê);
// empty means they pass. The reasons name what the engine read, never text
// that an agent wrote, but the path of a file and a word of the file, which
// are quoted.
func (r PolicyRules) Conditions(req Request, f PRFacts, hasFacts bool) []string {
	return r.conditions(req, f, hasFacts, true)
}

// conditions with thresholds=false keeps what no delegation lifts: the label and
// the diff the engine could not read.
func (r PolicyRules) conditions(req Request, f PRFacts, hasFacts, thresholds bool) []string {
	var out []string
	if req.Type == classes.RequestClassPermission || req.Type == "" {
		if req.Action != nil && thresholds {
			segs := append(commandSegments(req.Action.Command), strings.ToLower(strings.Join(strings.Fields(req.Action.Command), " ")))
			for _, p := range r.Commands {
				lp := strings.ToLower(p)
				for _, seg := range segs {
					if wildMatch(lp, seg) {
						out = append(out, fmt.Sprintf("comando sensível (%q)", p))
						break
					}
				}
			}
		}
		return out
	}
	if req.Type != classes.RequestPR && req.Type != classes.RequestMerge {
		return nil
	}
	if !hasFacts {
		return []string{ReasonIncompleteDiff + ": o motor não leu o PR"}
	}
	if !f.LabelsRead {
		out = append(out, ReasonIncompleteDiff+": os rótulos do PR não foram lidos")
	}
	for _, l := range f.Labels {
		if strings.EqualFold(l, HumanLabel) {
			out = append(out, "o PR tem o rótulo "+HumanLabel)
		}
	}
	if f.OpenedByGrandMaster {
		out = append(out, "o PR foi aberto pelo App Grão-Mestre, que não pode aprová-lo")
	}
	if f.Incomplete || len(f.Files) == 0 {
		out = append(out, ReasonIncompleteDiff+": o GitHub cortou a lista de arquivos")
	}
	for _, file := range f.Files {
		if file.PatchMissing && !r.ignored(file.Path) {
			out = append(out, fmt.Sprintf("%s: o GitHub não deu o patch de %q", ReasonIncompleteDiff, file.Path))
		}
	}
	if !thresholds {
		return out
	}
	if r.CIGreen && !f.CIGreen {
		out = append(out, "o CI não está verde neste hash")
	}
	if r.ReviewByOther && !f.ReviewDone {
		out = append(out, "falta a revisão concluída por outro personagem")
	}
	lines, deleted := 0, 0
	for _, file := range f.Files {
		ignored := r.ignored(file.Path)
		if file.Deleted {
			deleted++
		}
		if !ignored {
			lines += file.Added + file.Removed
		}
		for _, name := range r.areaNames() {
			for _, g := range r.Areas[name] {
				if classes.MatchGlob(g, file.Path) || (file.PreviousPath != "" && classes.MatchGlob(g, file.PreviousPath)) {
					out = append(out, fmt.Sprintf("área sensível %s: %q", name, file.Path))
					break
				}
			}
		}
		if ignored {
			continue
		}
		out = append(out, r.wordsIn(file)...)
	}
	if r.MaxFiles > 0 && len(f.Files) > r.MaxFiles {
		out = append(out, fmt.Sprintf("%d arquivos (o máximo é %d)", len(f.Files), r.MaxFiles))
	}
	if r.MaxLines > 0 && lines > r.MaxLines {
		out = append(out, fmt.Sprintf("%d linhas fora das ignoradas (o máximo é %d)", lines, r.MaxLines))
	}
	if r.MaxDeleted > 0 && deleted > r.MaxDeleted {
		out = append(out, fmt.Sprintf("%d arquivos apagados (o máximo é %d)", deleted, r.MaxDeleted))
	}
	return out
}

// wordsIn is the sensitive words in the added lines of a file, one reason per
// word.
func (r PolicyRules) wordsIn(file PRFile) []string {
	var out []string
	for i, re := range r.wordRes {
		for _, line := range file.AddedLines {
			if re.MatchString(line) {
				out = append(out, fmt.Sprintf("palavra sensível %q numa linha adicionada de %q", r.Words[i], file.Path))
				break
			}
		}
	}
	return out
}

// policyGate is the conditions of the policy that the Grão-Mestre's approval
// must pass: a refusal human_required that leaves the request where it is (he
// takes it to the people with `mestre approval ask`, or his time runs out).
func (s *Service) policyGate(ctx context.Context, r Request) *Refusal {
	rules := s.rulesFor(ctx, r)
	if why := rules.ForcesPeople(r); why != "" {
		return refuse(CodeHumanRequired, "approval.detail", "detail", oneLine(why))
	}
	// An alto request is the Grão-Mestre's only inside a delegation of the Owner
	// (checked by the level): the thresholds below are for medio, but the label
	// and a diff the engine could not read hold for every risk.
	thresholds := r.Risk.Risk != classes.RiskHigh
	var facts PRFacts
	has := false
	if (r.Type == classes.RequestPR || r.Type == classes.RequestMerge) && r.PR != nil && s.o.PRs != nil {
		f, err := s.o.PRs.PullRequest(ctx, r.Workspace, *r.PR)
		if err != nil {
			s.log.Warn("chain: the PR was not read", "request", r.ID, "err", err)
		} else {
			facts, has = f, true
		}
	}
	why := rules.conditions(r, facts, has, thresholds)
	if s.o.Freshness != nil && isPR(r.Type) && r.PR != nil {
		// a workspace with no command to verify with: nothing is approved alone
		switch w, err := s.o.Freshness.Unverifiable(ctx, r); {
		case err != nil:
			s.log.Warn("chain: the gate of freshness could not be checked", "request", r.ID, "err", err)
			why = append(why, "frescor: não foi possível conferir o gate (veja o log do motor)")
		case w != "":
			why = append(why, w)
		}
	}
	if len(why) > 0 {
		for i := range why {
			why[i] = oneLine(why[i])
		}
		return refuse(CodeHumanRequired, "approval.detail", "detail", strings.Join(why, "; "))
	}
	return nil
}
