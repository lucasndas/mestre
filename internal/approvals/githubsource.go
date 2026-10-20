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
	CommitStatus(ctx context.Context, repo, sha string) (string, error)
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
	// Characters are the logins of the Apps of the Mestre (the bots of the
	// characters): only a review by one of them is "another character"
	// (docs/aprovacoes/politica.md); without it no review counts.
	Characters func(ctx context.Context) (map[string]bool, error)
	// GrandMasterLogin is the login of the bot of the App "Grão-Mestre": a PR
	// it opened is flagged, because the same App can not approve it. Nil: the
	// engine has no such App and nobody is flagged.
	GrandMasterLogin func(ctx context.Context) (string, error)
	// BaseTip is the tip of the base of a table in the mirror: the live value of
	// the `base_sha` of a target the gate of freshness ran on
	// (docs/github/acoes-por-nivel.md#merge). Nil: such a target can not be
	// verified and the request is not approved.
	BaseTip func(ctx context.Context, workspace, table string) (string, error)
}

var (
	_ chain.PRSource = GitHubSource{}
	_ Targets        = GitHubSource{}
)

// maxPRFiles is the most files GitHub lists of a PR: a PR with that many was cut.
const maxPRFiles = 3000

// PRTarget is the target of a request of PR or merge: the repository, the
// number, the commit and the branch it goes into. The request is bound to its
// impression (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato).
func PRTarget(repo string, n int, head, base string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"repo": strings.ToLower(repo), "pr": n, "head": head, "base": base})
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
	if p.State != "open" || p.Merged {
		return chain.PRFacts{}, fmt.Errorf("#%d is not open", ref.Number)
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
			// the patch of a file has no header: a line that starts with +++ is a
			// line added that starts with ++
			if strings.HasPrefix(l, "+") {
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
	status, err := rd.CommitStatus(ctx, ref.Repo, p.HeadSHA)
	if err != nil {
		return chain.PRFacts{}, err
	}
	// green: something ran, every check is fine and the legacy statuses, if
	// there are any, are too
	facts.CIGreen = (len(checks) > 0 || status != "") && (status == "" || status == "success")
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
	var mine map[string]bool
	if g.Characters != nil {
		if mine, err = g.Characters(ctx); err != nil {
			return chain.PRFacts{}, err
		}
	}
	facts.ReviewDone = reviewDone(p.Author, p.HeadSHA, mine, reviews)
	if g.GrandMasterLogin != nil {
		login, err := g.GrandMasterLogin(ctx)
		if err != nil {
			return chain.PRFacts{}, err
		}
		facts.OpenedByGrandMaster = login == "" || strings.EqualFold(p.Author, login)
	}
	// the PR may have moved while it was read: the facts are of one commit
	if again, err := rd.PullData(ctx, ref.Repo, ref.Number); err != nil || again.HeadSHA != p.HeadSHA {
		return chain.PRFacts{}, fmt.Errorf("the head of #%d moved while it was read", ref.Number)
	}
	return facts, nil
}

// reviewDone: a character of the Mestre that is not the author reviewed the
// head (an approval, or a comment with nothing asked), and no reviewer is left
// with a request for changes. A request for changes is not lifted by a later
// comment, only by an approval or a dismissal; a review of another commit does
// not describe this one.
func reviewDone(author, head string, characters map[string]bool, reviews []github.Review) bool {
	last := map[string]string{}
	for _, r := range reviews {
		switch r.State {
		case "CHANGES_REQUESTED", "APPROVED", "DISMISSED":
			last[r.By] = r.State
			if r.State == "APPROVED" && r.Commit != head {
				last[r.By] = "STALE" // approved another commit: nothing to count, nothing asked
			}
		case "COMMENTED":
			if last[r.By] == "" {
				last[r.By] = "COMMENTED"
				if r.Commit != head {
					last[r.By] = "STALE"
				}
			}
		}
	}
	other := false
	for by, st := range last {
		if st == "CHANGES_REQUESTED" {
			return false
		}
		if by != author && characters[by] && (st == "APPROVED" || st == "COMMENTED") {
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
	target := PRTarget(r.PR.Repo, r.PR.Number, p.HeadSHA, p.BaseRef)
	if chain.BaseSHAOf(r.Target) == "" {
		// the gate did not run (the repository asks the branch to be up to date):
		// the base moving changes nothing
		return target, nil
	}
	if g.BaseTip == nil {
		return nil, errors.New("approvals: no way to read the tip of the base")
	}
	tip, err := g.BaseTip(ctx, r.Workspace, r.Table)
	if err != nil {
		return nil, err
	}
	return chain.WithBaseSHA(target, tip)
}
