package approvals_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
)

const grandMasterAppID = 777

func (l *ghLab) settings() *approvals.Settings {
	return &approvals.Settings{Requests: l.open, Modes: l.modes,
		Workspace: func(context.Context, string) (string, string, error) { return "lucas/meu-app", "main", nil },
		GrandMaster: func(context.Context, string) (approvals.SettingsClient, int64, error) {
			return l.grand, grandMasterAppID, nil
		},
		Tell: l.tell.toWorkspace}
}

func oneReview() fakegithub.Rule {
	return fakegithub.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": 2, "require_code_owner_review": true}}
}

// CA-GH-APROV-02
func TestThePlanBProposesARulesetWithOnlyTheReviewAndTheAppAsAnException(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.gh.SetBranchRules("lucas/meu-app", "main", oneReview(),
		fakegithub.Rule{Type: "required_status_checks", Parameters: map[string]any{"strict_required_status_checks_policy": true}})
	id, err := l.settings().ProposePlanB(l.ctx, "loja")
	if err != nil || id == 0 {
		t.Fatalf("ProposePlanB = %d, %v", id, err)
	}
	made := l.open.all()
	if len(made) != 1 || made[0].Type != classes.RequestRepoSettings || made[0].Workspace != "loja" {
		t.Fatalf("requests = %+v; want one configuracao_repositorio", made)
	}
	tg := decodeTarget(t, made[0].Target)
	if tg.Kind != approvals.KindRuleset || tg.Repo != "lucas/meu-app" || !tg.PlanB {
		t.Fatalf("target = %+v", tg)
	}
	rules, _ := tg.Body["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules of the ruleset = %v; want only the review (the CI stays where it is)", tg.Body["rules"])
	}
	rule := rules[0].(map[string]any)
	params := rule["parameters"].(map[string]any)
	if rule["type"] != "pull_request" || params["required_approving_review_count"] != float64(2) || params["require_code_owner_review"] != true {
		t.Fatalf("the rule = %v; want the review as the repository asks it today", rule)
	}
	actors, _ := tg.Body["bypass_actors"].([]any)
	if len(actors) != 1 {
		t.Fatalf("bypass actors = %v; want only the App", tg.Body["bypass_actors"])
	}
	actor := actors[0].(map[string]any)
	if actor["actor_id"] != float64(grandMasterAppID) || actor["actor_type"] != "Integration" || actor["bypass_mode"] != "pull_request" {
		t.Fatalf("bypass actor = %v; want the App Grão-Mestre as Integration with pull_request", actor)
	}
	// pending: only the Owner answers, with the PIN, and GitHub is untouched
	req := chain.Request{Type: classes.RequestRepoSettings, Risk: classes.RiskResult{Risk: classes.RiskHigh}}
	rl := chain.DefaultRules()
	if _, _, min := rl.Need(req); min != classes.LevelOwner || !rl.NeedsPIN(req) {
		t.Fatalf("the request asks level %q (pin %v); want the Owner with PIN", min, rl.NeedsPIN(req))
	}
	if got := l.gh.Rulesets("lucas/meu-app"); len(got) != 0 {
		t.Fatalf("rulesets created before the approval: %v", got)
	}
	if mode, _, _ := l.modes.Get(l.ctx, "loja"); mode != approvals.ModeApp {
		t.Fatalf("mode = %q before the Owner answered", mode)
	}
}

func TestThePlanBSaysWhereTheReviewIsAskedTodayAndNeedsARequirement(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	if _, err := l.settings().ProposePlanB(l.ctx, "loja"); !errors.Is(err, approvals.ErrNoRequirement) {
		t.Fatalf("ProposePlanB with no requirement = %v; want ErrNoRequirement", err)
	}
	l.gh.SetProtection("lucas/meu-app", "main", map[string]any{"required_pull_request_reviews": map[string]any{"required_approving_review_count": 1}})
	if _, err := l.settings().ProposePlanB(l.ctx, "loja"); err != nil {
		t.Fatal(err)
	}
	if about := l.open.all()[0].About; !strings.Contains(about, "proteção clássica") {
		t.Fatalf("about = %q; want the change of the classic protection", about)
	}
	rules, _ := l.grand.ReadRules(l.ctx, "lucas/meu-app", "main")
	if !rules.Classic || !strings.Contains(github.PlanBChange(rules), "Tirar a exigência de revisão da proteção clássica") {
		t.Fatalf("rules = %+v", rules)
	}
}

// CA-GH-APROV-03
func TestThePlanBOnAPrivateRepositoryWithoutProLeavesTheWorkspaceInHumanApproval(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.gh.SetBranchRules("lucas/meu-app", "main", oneReview())
	s := l.settings()
	if _, err := s.ProposePlanB(l.ctx, "loja"); err != nil {
		t.Fatal(err)
	}
	made := l.open.all()[0]
	// the Owner approves; GitHub refuses the ruleset of a private repository without Pro
	err := s.Decided(l.ctx, chain.Result{Request: chain.Request{ID: 101, Workspace: "loja", Type: classes.RequestRepoSettings, Target: made.Target}, Approved: true})
	if err != nil {
		t.Fatalf("Decided = %v; the refusal of GitHub is the mode of human approval, not an error", err)
	}
	mode, why, err := l.modes.Get(l.ctx, "loja")
	if err != nil || mode != approvals.ModeHuman || !strings.Contains(why, "Pro") {
		t.Fatalf("mode = %q (%q), %v; want human approval because of the plan", mode, why, err)
	}
	told := l.tell.toOwner()
	if len(told) != 1 || !strings.Contains(told[0], "modo de aprovação humana") || !strings.Contains(told[0], "Pro") {
		t.Fatalf("the Owner was told %q; want the mode and the reason", told)
	}
	if got := l.gh.Rulesets("lucas/meu-app"); len(got) != 0 {
		t.Fatalf("rulesets = %v", got)
	}
}

func TestThePlanBWithProCreatesTheRulesetAndTheOwnersDenialIsHumanApproval(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.gh.SetBranchRules("lucas/meu-app", "main", oneReview())
	l.gh.SetPro("lucas/meu-app", true)
	s := l.settings()
	if _, err := s.ProposePlanB(l.ctx, "loja"); err != nil {
		t.Fatal(err)
	}
	made := l.open.all()[0]
	res := chain.Result{Request: chain.Request{ID: 101, Workspace: "loja", Type: classes.RequestRepoSettings, Target: made.Target}}
	// the Owner says no: no ruleset, human approval
	if err := s.Decided(l.ctx, res); err != nil {
		t.Fatal(err)
	}
	if mode, _, _ := l.modes.Get(l.ctx, "loja"); mode != approvals.ModeHuman {
		t.Fatalf("mode after the denial = %q; want human approval", mode)
	}
	if got := l.gh.Rulesets("lucas/meu-app"); len(got) != 0 {
		t.Fatalf("rulesets after a denial = %v", got)
	}
	// the Owner says yes (another workspace state): the ruleset exists
	if err := l.modes.Set(l.ctx, "loja", approvals.ModeApp, ""); err != nil {
		t.Fatal(err)
	}
	res.Approved = true
	if err := s.Decided(l.ctx, res); err != nil {
		t.Fatal(err)
	}
	got := l.gh.Rulesets("lucas/meu-app")
	if len(got) != 1 || got[0]["name"] != github.RulesetName {
		t.Fatalf("rulesets = %v; want mestre-aprovacao", got)
	}
	if mode, _, _ := l.modes.Get(l.ctx, "loja"); mode != approvals.ModeApp {
		t.Fatalf("mode = %q after the ruleset", mode)
	}
}

// A request of settings of another kind is not the plan B's to apply.
func TestSettingsDoNotApplyWhatTheyDoNotKnow(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	err := l.settings().Decided(l.ctx, chain.Result{Request: chain.Request{Workspace: "loja", Type: classes.RequestRepoSettings, Target: []byte(`{"kind":"protection","repo":"lucas/meu-app"}`)}, Approved: true})
	if err != nil || len(l.gh.Actions()) != 0 {
		t.Fatalf("Decided = %v with %d writes; want nothing", err, len(l.gh.Actions()))
	}
}
