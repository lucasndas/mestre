package github

import (
	"context"
	"errors"
	"fmt"
)

// The names the approval test uses
// (docs/github/aprovacao-no-github.md#teste-na-sessão-zero-o-review-do-app-conta).
const (
	// TestBranch is the branch of the test, deleted when it ends.
	TestBranch = "mestre/teste-aprovacao"
	// TestTitle is the title of the pull request of the test.
	TestTitle = "[Mestre] Teste de aprovação (pode ignorar)"
	// TestFile is the file the test branch adds.
	TestFile = ".mestre-teste-aprovacao"
)

// ProbePR is the pull request of the test.
type ProbePR struct {
	Number int
	Branch string
}

// ReviewProbe is GitHub's side of the approval test of the session zero: the
// App "Mestre" opens a pull request and the App "Grão-Mestre" approves it, to
// see whether the repository counts the review of an App.
type ReviewProbe struct {
	// Mestre acts as the App "Mestre" and GrandMaster as the App "Grão-Mestre".
	Mestre, GrandMaster *REST
}

// ReadRules reads what the main branch asks of a pull request.
func (p ReviewProbe) ReadRules(ctx context.Context, repo, branch string) (ReviewRules, error) {
	return p.GrandMaster.ReadRules(ctx, repo, branch)
}

// Open makes the test branch from the tip of the base, with the test file, and
// opens the pull request. When it fails halfway it still returns what it
// created, for Close. A test branch left by an earlier run is the Mestre's own
// (the name is reserved) and is cleared first.
func (p ReviewProbe) Open(ctx context.Context, repo, base string) (ProbePR, error) {
	pr := ProbePR{Branch: TestBranch}
	if err := p.Mestre.DeleteBranch(ctx, repo, TestBranch); err != nil {
		return ProbePR{}, fmt.Errorf("clear an earlier test branch: %w", err)
	}
	tip, err := p.Mestre.RefSHA(ctx, repo, base)
	if err != nil {
		return ProbePR{}, fmt.Errorf("read the base: %w", err)
	}
	if err := p.Mestre.CreateBranch(ctx, repo, TestBranch, tip); err != nil {
		return ProbePR{}, fmt.Errorf("create the test branch: %w", err)
	}
	if err := p.Mestre.PutFile(ctx, repo, TestBranch, TestFile, "Arquivo do teste de aprovação do Mestre; pode ser ignorado.\n", "chore: teste de aprovação do Mestre"); err != nil {
		return pr, fmt.Errorf("write the test file: %w", err)
	}
	pull, err := p.Mestre.CreatePull(ctx, repo, TestTitle, "Teste automático do Mestre: confere se o review do App \"Grão-Mestre\" conta para as revisões obrigatórias. Este PR é fechado sozinho.", TestBranch, base, false)
	if err != nil {
		return pr, fmt.Errorf("open the test pull request: %w", err)
	}
	pr.Number = pull.Number
	return pr, nil
}

// Approve has the Grão-Mestre send a review APPROVE on the head of the pull request.
func (p ReviewProbe) Approve(ctx context.Context, repo string, pr ProbePR) error {
	pull, err := p.GrandMaster.PullData(ctx, repo, pr.Number)
	if err != nil {
		return err
	}
	_, err = p.GrandMaster.CreateReview(ctx, repo, pr.Number, pull.HeadSHA, ReviewApprove, "Aprovação do teste do Mestre (pode ignorar).")
	return err
}

// Decision is the reviewDecision of the pull request.
func (p ReviewProbe) Decision(ctx context.Context, repo string, pr ProbePR) (string, error) {
	return p.GrandMaster.ReviewDecision(ctx, repo, pr.Number)
}

// Close closes the pull request without merging and deletes the branch. Both
// are tried and what is already gone is not an error.
func (p ReviewProbe) Close(ctx context.Context, repo string, pr ProbePR) error {
	var errs []error
	if pr.Number > 0 {
		if err := p.Mestre.ClosePull(ctx, repo, pr.Number); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if pr.Branch != "" {
		if err := p.Mestre.DeleteBranch(ctx, repo, pr.Branch); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
