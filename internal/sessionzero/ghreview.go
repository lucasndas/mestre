package sessionzero

import (
	"context"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// GitHubReview is the AppReview over GitHub: the steps of the approval test as
// docs/github/aprovacao-no-github.md lists them, with the App "Mestre" opening
// the pull request and the App "Grão-Mestre" reviewing it.
type GitHubReview struct {
	Probe github.ReviewProbe
}

var _ AppReview = GitHubReview{}

func fullName(r workspaces.Repo) string { return r.Owner + "/" + r.Name }

// ReadRules implements AppReview.
func (g GitHubReview) ReadRules(ctx context.Context, repo workspaces.Repo, branch string) (Rules, error) {
	r, err := g.Probe.ReadRules(ctx, fullName(repo), branch)
	if err != nil {
		return Rules{}, err
	}
	return Rules{RequiresReview: r.Requires(), Summary: r.Summary}, nil
}

// OpenTestPR implements AppReview.
func (g GitHubReview) OpenTestPR(ctx context.Context, repo workspaces.Repo, base string) (TestPR, error) {
	pr, err := g.Probe.Open(ctx, fullName(repo), base)
	return TestPR{Number: pr.Number, Branch: pr.Branch}, err
}

// ApproveAsGrandMaster implements AppReview.
func (g GitHubReview) ApproveAsGrandMaster(ctx context.Context, repo workspaces.Repo, pr TestPR) error {
	return g.Probe.Approve(ctx, fullName(repo), github.ProbePR{Number: pr.Number, Branch: pr.Branch})
}

// ReviewDecision implements AppReview.
func (g GitHubReview) ReviewDecision(ctx context.Context, repo workspaces.Repo, pr TestPR) (string, error) {
	return g.Probe.Decision(ctx, fullName(repo), github.ProbePR{Number: pr.Number, Branch: pr.Branch})
}

// CloseTestPR implements AppReview.
func (g GitHubReview) CloseTestPR(ctx context.Context, repo workspaces.Repo, pr TestPR) error {
	return g.Probe.Close(ctx, fullName(repo), github.ProbePR{Number: pr.Number, Branch: pr.Branch})
}
