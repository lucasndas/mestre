package sessionzero

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// reviewApproved is the reviewDecision of a pull request whose required
// reviews are met.
const reviewApproved = "APPROVED"

// runApprovalTest is the stage approval_test: whether the review of the App
// "Grão-Mestre" counts for the required reviews of the repository. The steps
// are those of docs/github/aprovacao-no-github.md (the document owns them);
// the result is recorded for the panel and ends the stage, whatever it is
// (nao_conta leads to plan B, which is not the session zero's).
func (s *Service) runApprovalTest(ctx context.Context, w workspaces.Workspace, force bool) error {
	st, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	if st.ApprovalTest != "" && !force {
		return nil // already recorded: a retried stage does not open another pull request
	}
	rules, err := s.o.AppReview.ReadRules(ctx, w.Repo, w.DefaultBranch)
	if err != nil {
		return code("approval_rules_failed", err)
	}
	result := ResultNoRequirement
	if rules.RequiresReview {
		if result, err = s.probeReview(ctx, w); err != nil {
			return code("approval_test_failed", err)
		}
	}
	if err := s.store.ApprovalTested(ctx, w.Slug, s.o.Clock.Now(), result, rules.Summary); err != nil {
		return err
	}
	s.log.Info("session zero: approval test", "workspace", w.Slug, "result", result)
	return nil
}

// RunApprovalTest runs the test again and records the new result: for a merge
// approved by the App that the repository refused for lack of review
// (docs/github/aprovacao-no-github.md).
func (s *Service) RunApprovalTest(ctx context.Context, slug string) error {
	w, err := s.o.Workspaces.Get(ctx, slug)
	if err != nil {
		return err
	}
	return s.runApprovalTest(ctx, w, true)
}

// probeReview opens the test pull request, has the App approve it and reads
// whether the repository counts it. The pull request is closed and its branch
// deleted whatever happens, even when a step failed halfway; if that cleanup
// fails the test fails (and is tried again) rather than leaving a branch of
// the Mestre in the project of the person.
func (s *Service) probeReview(ctx context.Context, w workspaces.Workspace) (result string, err error) {
	pr, err := s.o.AppReview.OpenTestPR(ctx, w.Repo, w.DefaultBranch)
	if pr != (TestPR{}) {
		defer func() {
			// The cleanup runs even if the stage was cancelled.
			if cerr := s.o.AppReview.CloseTestPR(context.WithoutCancel(ctx), w.Repo, pr); cerr != nil {
				result, err = "", errors.Join(err, fmt.Errorf("close the test pull request: %w", cerr))
			}
		}()
	}
	if err != nil {
		return "", fmt.Errorf("open the test pull request: %w", err)
	}
	if err := s.o.AppReview.ApproveAsGrandMaster(ctx, w.Repo, pr); err != nil {
		return "", fmt.Errorf("approve as the Grão-Mestre: %w", err)
	}
	decision, err := s.o.AppReview.ReviewDecision(ctx, w.Repo, pr)
	if err != nil {
		return "", fmt.Errorf("read the review decision: %w", err)
	}
	if decision == reviewApproved {
		return ResultCounts, nil
	}
	return ResultDoesNotCount, nil
}
