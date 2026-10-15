package sessionzero

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// reviewSteps is an AppReview that records the steps and fails on demand.
type reviewSteps struct {
	steps     []string
	failAt    string
	closeErr  error
	decision  string
	cancelAt  string
	cancelCtx context.CancelFunc
}

func (r *reviewSteps) step(name string) error {
	r.steps = append(r.steps, name)
	if r.cancelAt == name {
		r.cancelCtx()
	}
	if r.failAt == name {
		return errors.New("falhou em " + name)
	}
	return nil
}

func (r *reviewSteps) ReadRules(context.Context, workspaces.Repo, string) (Rules, error) {
	return Rules{RequiresReview: true}, r.step("rules")
}

func (r *reviewSteps) OpenTestPR(context.Context, workspaces.Repo, string) (TestPR, error) {
	return TestPR{Number: 3, Branch: "mestre/teste-aprovacao"}, r.step("open")
}

func (r *reviewSteps) ApproveAsGrandMaster(context.Context, workspaces.Repo, TestPR) error {
	return r.step("approve")
}

func (r *reviewSteps) ReviewDecision(context.Context, workspaces.Repo, TestPR) (string, error) {
	return r.decision, r.step("decision")
}

func (r *reviewSteps) CloseTestPR(ctx context.Context, _ workspaces.Repo, _ TestPR) error {
	if ctx.Err() != nil {
		return ctx.Err() // the cleanup must not run on a cancelled context
	}
	r.steps = append(r.steps, "close")
	return r.closeErr
}

func TestProbeReviewStepsAndCleanup(t *testing.T) {
	t.Parallel()
	repo := workspaces.Repo{Owner: "lucas", Name: "meu-app"}
	for name, tc := range map[string]struct {
		r      *reviewSteps
		result string
		steps  []string
		fails  bool
	}{
		"counts":      {r: &reviewSteps{decision: "APPROVED"}, result: ResultCounts, steps: []string{"open", "approve", "decision", "close"}},
		"does not":    {r: &reviewSteps{decision: "REVIEW_REQUIRED"}, result: ResultDoesNotCount, steps: []string{"open", "approve", "decision", "close"}},
		"no decision": {r: &reviewSteps{decision: ""}, result: ResultDoesNotCount, steps: []string{"open", "approve", "decision", "close"}},
		"open fails":  {r: &reviewSteps{failAt: "open"}, steps: []string{"open", "close"}, fails: true},
		"approve":     {r: &reviewSteps{failAt: "approve"}, steps: []string{"open", "approve", "close"}, fails: true},
		"decision":    {r: &reviewSteps{failAt: "decision"}, steps: []string{"open", "approve", "decision", "close"}, fails: true},
		"close fails": {r: &reviewSteps{decision: "APPROVED", closeErr: errors.New("sem permissão")}, steps: []string{"open", "approve", "decision", "close"}, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &Service{o: Options{AppReview: tc.r}}
			result, err := s.probeReview(t.Context(), workspaces.Workspace{Repo: repo, DefaultBranch: "main"})
			if (err != nil) != tc.fails || result != tc.result {
				t.Fatalf("probeReview = %q, %v; want %q, fails=%v", result, err, tc.result, tc.fails)
			}
			if !slices.Equal(tc.r.steps, tc.steps) {
				t.Fatalf("steps = %v; want %v", tc.r.steps, tc.steps)
			}
		})
	}
}

// The branch of the test is deleted even when the stage was cancelled halfway.
func TestProbeReviewCleansUpAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	r := &reviewSteps{decision: "APPROVED", cancelAt: "approve", cancelCtx: cancel}
	s := &Service{o: Options{AppReview: r}}
	_, _ = s.probeReview(ctx, workspaces.Workspace{Repo: workspaces.Repo{Owner: "lucas", Name: "meu-app"}, DefaultBranch: "main"})
	if !slices.Contains(r.steps, "close") {
		t.Fatalf("steps = %v; the test pull request was left open", r.steps)
	}
}
