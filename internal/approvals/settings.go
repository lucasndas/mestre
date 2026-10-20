package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// SettingsClient is the part of GitHub the settings of the repository use
// (github.REST with the token of the App "Grão-Mestre").
type SettingsClient interface {
	ReadRules(ctx context.Context, repo, branch string) (github.ReviewRules, error)
	CreateRuleset(ctx context.Context, repo string, body map[string]any) error
}

var _ SettingsClient = (*github.REST)(nil)

// Kinds of change of the settings of a repository.
const (
	// KindRuleset: a ruleset to create (the body is the one of the API).
	KindRuleset = "ruleset"
)

// SettingsTarget is the exact target of a request `configuracao_repositorio`: what
// the Owner approves is what is applied.
type SettingsTarget struct {
	Kind   string         `json:"kind"`
	Repo   string         `json:"repo"`
	Branch string         `json:"branch,omitempty"`
	Body   map[string]any `json:"body"`
	// PlanB says the change is the plan B of the approval test: a refusal sends
	// the workspace to the mode of human approval.
	PlanB bool `json:"plan_b,omitempty"`
}

// ErrNoRequirement: the repository asks for no review, so there is nothing for the
// plan B to let through.
var ErrNoRequirement = errors.New("approvals: the main branch asks for no review")

// Settings is the change of the settings of a repository that only the Owner
// approves, and the plan B of the approval test.
type Settings struct {
	Requests Opener
	Modes    *Modes
	// Workspace gives the repository and the main branch of a workspace.
	Workspace func(ctx context.Context, workspace string) (repo, branch string, err error)
	// GrandMaster gives the client of the App "Grão-Mestre" and its id.
	GrandMaster func(ctx context.Context, workspace string) (SettingsClient, int64, error)
	// Tell warns the Owner (the bot and the panel).
	Tell   func(ctx context.Context, workspace, text string)
	Logger *slog.Logger
}

// ProposePlanB makes the request to the Owner for the ruleset of the plan B,
// after the approval test said the review of the App does not count. The request
// is of the type configuracao_repositorio, which asks the PIN and only the Owner
// answers. It returns the number of the request.
func (s *Settings) ProposePlanB(ctx context.Context, workspace string) (int64, error) {
	repo, branch, err := s.Workspace(ctx, workspace)
	if err != nil {
		return 0, err
	}
	gm, appID, err := s.GrandMaster(ctx, workspace)
	if err != nil {
		return 0, err
	}
	rules, err := gm.ReadRules(ctx, repo, branch)
	if err != nil {
		return 0, err
	}
	if !rules.Requires() {
		return 0, ErrNoRequirement
	}
	target, err := json.Marshal(SettingsTarget{Kind: KindRuleset, Repo: repo, Branch: branch, Body: github.PlanBRuleset(rules, branch, appID), PlanB: true})
	if err != nil {
		return 0, err
	}
	return s.Requests.Request(ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: workspace, Target: target,
		About: "plano B da aprovação: " + github.PlanBChange(rules)})
}

// Decided implements chain.Sink for the requests of settings: an approved ruleset
// is created; a refusal of the Owner, or of GitHub because the account has no Pro
// for a private repository, leaves the workspace in the mode of human approval.
func (s *Settings) Decided(ctx context.Context, r chain.Result) error {
	var t SettingsTarget
	if json.Unmarshal(r.Request.Target, &t) != nil || t.Kind != KindRuleset {
		return nil // a change of another kind is not applied here
	}
	ws := r.Request.Workspace
	if !r.Approved {
		if t.PlanB {
			return s.human(ctx, ws, "o Dono não aprovou o ruleset de exceção para o Grão-Mestre")
		}
		return nil
	}
	gm, _, err := s.GrandMaster(ctx, ws)
	if err != nil {
		return err
	}
	err = gm.CreateRuleset(ctx, t.Repo, t.Body)
	var api *github.APIError
	switch {
	case err == nil:
		s.tell(ctx, ws, fmt.Sprintf("O ruleset %q foi criado no repositório %s.", github.RulesetName, t.Repo))
		return nil
	case t.PlanB && errors.As(err, &api) && api.Status == http.StatusForbidden:
		return s.human(ctx, ws, "o GitHub não aceita rulesets em repositório privado sem o plano Pro ("+api.Message+")")
	case t.PlanB:
		s.tell(ctx, ws, fmt.Sprintf("O ruleset de exceção do repositório %s não foi criado: %v.", t.Repo, err))
		return err
	}
	return err
}

func (s *Settings) human(ctx context.Context, workspace, why string) error {
	if err := s.Modes.Set(ctx, workspace, ModeHuman, why); err != nil {
		return err
	}
	s.tell(ctx, workspace, fmt.Sprintf("O workspace %s passou ao modo de aprovação humana: %s. Cada merge vai pedir a aprovação de uma pessoa pelo Telegram.", workspace, why))
	return nil
}

func (s *Settings) tell(ctx context.Context, workspace, text string) {
	if s.Tell != nil {
		s.Tell(ctx, workspace, text)
	}
}
