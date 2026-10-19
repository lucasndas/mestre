package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// PullReader is the GitHub the facts of a PR are read from, with the App of the
// Grão-Mestre (github.REST).
type PullReader interface {
	PullData(ctx context.Context, repo string, n int) (github.PullData, error)
	PullFiles(ctx context.Context, repo string, n int) ([]github.PullFile, error)
	CheckRuns(ctx context.Context, repo, sha string) ([]github.Check, error)
	Reviews(ctx context.Context, repo string, n int) ([]github.Review, error)
	IssueData(ctx context.Context, repo string, n int) (github.IssueData, error)
}

var _ PullReader = (*github.REST)(nil)

// GitHubSource reads a PR for the conditions of the policy (chain.PRSource) and
// the live target of a request of PR (Targets). Nothing it gives comes from an
// agent: it is what GitHub says of the commit of the request.
type GitHubSource struct {
	// Reader gives the reader of the repository of a workspace.
	Reader func(ctx context.Context, workspace string) (PullReader, error)
}

var (
	_ chain.PRSource = GitHubSource{}
	_ Targets        = GitHubSource{}
)

// maxPRFiles is the most files GitHub lists of a PR: a PR with that many was cut.
const maxPRFiles = 3000

// PRTarget is the target of a request of PR or merge: the repository, the
// number and the commit. The request is bound to its impression
// (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato).
func PRTarget(repo string, n int, sha string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"repo": strings.ToLower(repo), "pr": n, "head": sha})
	return b
}

// PullRequest implements chain.PRSource. The facts are those of the commit of the
// request: a PR whose head moved has no facts at that commit, and the decision
// goes to a person.
func (g GitHubSource) PullRequest(ctx context.Context, workspace string, ref chain.PRRef) (chain.PRFacts, error) {
	rd, err := g.Reader(ctx, workspace)
	if err != nil {
		return chain.PRFacts{}, err
	}
	p, err := rd.PullData(ctx, ref.Repo, ref.Number)
	if err != nil {
		return chain.PRFacts{}, err
	}
	if p.HeadSHA != ref.SHA {
		return chain.PRFacts{}, fmt.Errorf("the head of #%d is %.8s, not the %.8s of the request", ref.Number, p.HeadSHA, ref.SHA)
	}
	files, err := rd.PullFiles(ctx, ref.Repo, ref.Number)
	if err != nil {
		return chain.PRFacts{}, err
	}
	facts := chain.PRFacts{Incomplete: len(files) >= maxPRFiles}
	for _, f := range files {
		cf := chain.PRFile{Path: f.Path, PreviousPath: f.PreviousPath, Added: f.Additions, Removed: f.Deletions, Deleted: f.Status == "removed"}
		// text with changes and no patch: GitHub cut it, and the engine can not read it
		cf.PatchMissing = f.Patch == "" && f.Additions+f.Deletions > 0 && !cf.Deleted
		for _, l := range strings.Split(f.Patch, "\n") {
			if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
				cf.AddedLines = append(cf.AddedLines, strings.TrimPrefix(l, "+"))
			}
		}
		facts.Files = append(facts.Files, cf)
	}
	issue, err := rd.IssueData(ctx, ref.Repo, ref.Number)
	if err != nil {
		return chain.PRFacts{}, err
	}
	facts.Labels, facts.LabelsRead = issue.Labels, true
	checks, err := rd.CheckRuns(ctx, ref.Repo, p.HeadSHA)
	if err != nil {
		return chain.PRFacts{}, err
	}
	facts.CIGreen = len(checks) > 0
	for _, c := range checks {
		switch c.State {
		case "success", "neutral", "skipped":
		default:
			facts.CIGreen = false
		}
	}
	reviews, err := rd.Reviews(ctx, ref.Repo, ref.Number)
	if err != nil {
		return chain.PRFacts{}, err
	}
	facts.ReviewDone = reviewDone(p.Author, reviews)
	return facts, nil
}

// reviewDone: another character (a bot that is not the author) reviewed and no
// reviewer's last word is a request for changes.
func reviewDone(author string, reviews []github.Review) bool {
	last := map[string]string{}
	for _, r := range reviews {
		if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" || r.State == "COMMENTED" || r.State == "DISMISSED" {
			last[r.By] = r.State
		}
	}
	other := false
	for by, st := range last {
		if st == "CHANGES_REQUESTED" {
			return false
		}
		if by != author && strings.HasSuffix(by, "[bot]") && (st == "APPROVED" || st == "COMMENTED") {
			other = true
		}
	}
	return other
}

// Current implements Targets: the live target of a request of PR or merge, in
// the form PRTarget makes it.
func (g GitHubSource) Current(ctx context.Context, r chain.Request) (json.RawMessage, error) {
	if r.PR == nil || (r.Type != classes.RequestPR && r.Type != classes.RequestMerge) {
		return nil, errors.New("approvals: no PR to read the target of")
	}
	rd, err := g.Reader(ctx, r.Workspace)
	if err != nil {
		return nil, err
	}
	p, err := rd.PullData(ctx, r.PR.Repo, r.PR.Number)
	if err != nil {
		return nil, err
	}
	return PRTarget(r.PR.Repo, r.PR.Number, p.HeadSHA), nil
}
