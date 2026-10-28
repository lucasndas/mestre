package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
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

// ErrNoPlanB: the plan B would loosen a rule, so the workspace went to human approval.
var ErrNoPlanB = errors.New("approvals: the plan B would loosen a rule of the repository")

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
	Tell   func(ctx context.Context, workspace string, m i18n.Msg)
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
	if rules.CodeOwners || rules.LastPush {
		// the exception skips every rule of its ruleset: the code owner review and the
		// approval of the last push would stop being enforced for the Grão-Mestre
		why := i18n.M("approval.why_code_owner")
		if err := s.human(ctx, workspace, why); err != nil {
			return 0, err
		}
		return 0, ErrNoPlanB
	}
	target, err := json.Marshal(SettingsTarget{Kind: KindRuleset, Repo: repo, Branch: branch, Body: github.PlanBRuleset(rules, branch, appID), PlanB: true})
	if err != nil {
		return 0, err
	}
	return s.Requests.Request(ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: workspace, Target: target,
		About: "plano B da aprovação: " + github.PlanBChange(rules)})
}

// ErrBadRuleset: the body is not the body of a ruleset of the API of GitHub.
var ErrBadRuleset = errors.New("approvals: the change is not a ruleset (a JSON object with name, enforcement and rules)")

// maxRulesetBytes is the largest body of a ruleset that is put to the Owner.
const maxRulesetBytes = 32 << 10

// rulesetKeys are the fields of the body of a ruleset the Grão-Mestre may propose: what
// is not here is not put to the Owner.
var rulesetKeys = map[string]bool{"name": true, "target": true, "enforcement": true, "rules": true, "bypass_actors": true, "conditions": true}

// bypassActorTypes are the kinds of actor a ruleset may let bypass it.
var bypassActorTypes = map[string]bool{"Integration": true, "OrganizationAdmin": true, "RepositoryRole": true, "Team": true, "DeployKey": true}

// checkRuleset validates the body of a ruleset by an allow list, so that what the Owner
// reads (summarized) is all there is in it.
func checkRuleset(body map[string]any) error {
	for k := range body {
		if !rulesetKeys[k] {
			return fmt.Errorf("%w: the field %q is not accepted", ErrBadRuleset, k)
		}
	}
	if name, _ := body["name"].(string); strings.TrimSpace(name) == "" || len(name) > 100 {
		return ErrBadRuleset
	}
	if enf, _ := body["enforcement"].(string); enf != "active" && enf != "evaluate" && enf != "disabled" {
		return fmt.Errorf("%w: enforcement is active, evaluate or disabled", ErrBadRuleset)
	}
	if t, ok := body["target"]; ok && t != "branch" && t != "tag" {
		return fmt.Errorf("%w: target is branch or tag", ErrBadRuleset)
	}
	rules, ok := body["rules"].([]any)
	if !ok {
		return ErrBadRuleset
	}
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if t, _ := m["type"].(string); !ok || t == "" {
			return fmt.Errorf("%w: every rule has a type", ErrBadRuleset)
		}
	}
	if ba, ok := body["bypass_actors"]; ok {
		list, ok := ba.([]any)
		if !ok {
			return ErrBadRuleset
		}
		for _, x := range list {
			m, ok := x.(map[string]any)
			if !ok {
				return ErrBadRuleset
			}
			id, isNum := m["actor_id"].(float64)
			typ, _ := m["actor_type"].(string)
			mode, _ := m["bypass_mode"].(string)
			if !isNum || id != float64(int64(id)) || !bypassActorTypes[typ] || (mode != "always" && mode != "pull_request") {
				return fmt.Errorf("%w: a bypass actor has an integer actor_id, a known actor_type and a bypass_mode always or pull_request", ErrBadRuleset)
			}
		}
	}
	if c, ok := body["conditions"]; ok {
		m, ok := c.(map[string]any)
		if !ok {
			return ErrBadRuleset
		}
		for k, v := range m {
			rn, ok := v.(map[string]any)
			if k != "ref_name" || !ok {
				return fmt.Errorf("%w: only conditions.ref_name is accepted", ErrBadRuleset)
			}
			for k2, v2 := range rn {
				list, ok := v2.([]any)
				if k2 != "include" && k2 != "exclude" || !ok {
					return fmt.Errorf("%w: ref_name takes include and exclude lists", ErrBadRuleset)
				}
				for _, p := range list {
					if _, ok := p.(string); !ok {
						return ErrBadRuleset
					}
				}
			}
		}
	}
	return nil
}

// describeRuleset is the text the Owner reads of the body he approves: every rule, the
// conditions and every exception with its type, id and mode (it is text of an agent, not
// verified: the body itself is the target he approves).
func describeRuleset(body map[string]any) string {
	var rules []string
	for _, r := range body["rules"].([]any) {
		rules = append(rules, r.(map[string]any)["type"].(string))
	}
	out := fmt.Sprintf("ruleset %q (%v, aplicação %v): regras [%s]", body["name"], orDefault(body["target"], "branch"), body["enforcement"], strings.Join(rules, ", "))
	if c, ok := body["conditions"].(map[string]any); ok {
		raw, _ := json.Marshal(c["ref_name"])
		out += "; refs " + string(raw)
	} else {
		out += "; refs: as do GitHub por padrão (nenhuma condição dada)"
	}
	if ba, _ := body["bypass_actors"].([]any); len(ba) > 0 {
		var ex []string
		for _, x := range ba {
			m := x.(map[string]any)
			ex = append(ex, fmt.Sprintf("%v #%.0f (%v)", m["actor_type"], m["actor_id"], m["bypass_mode"]))
		}
		out += "; exceções: " + strings.Join(ex, ", ")
	} else {
		out += "; sem exceções"
	}
	return out
}

func orDefault(v any, d string) any {
	if v == nil {
		return d
	}
	return v
}

// ProposeRuleset makes the request to the Owner for a ruleset the Grão-Mestre wants in
// the repository of the workspace (`mestre gh settings apply`). The body is checked by an
// allow list; the target is the exact body, so what the Owner approves, with the PIN, is
// what is created, and the text he reads says every rule, the conditions and each
// exception. The repository is the one of the workspace, never one the caller names, and
// proposer (the Grão-Mestre) decides nothing of the request. It returns the number of the
// request; nothing is changed until the Owner approves.
func (s *Settings) ProposeRuleset(ctx context.Context, workspace, proposer string, body map[string]any) (int64, error) {
	if err := checkRuleset(body); err != nil {
		return 0, err
	}
	if raw, err := json.Marshal(body); err != nil || len(raw) > maxRulesetBytes {
		return 0, fmt.Errorf("%w: it is too large", ErrBadRuleset)
	}
	repo, branch, err := s.Workspace(ctx, workspace)
	if err != nil {
		return 0, err
	}
	target, err := json.Marshal(SettingsTarget{Kind: KindRuleset, Repo: repo, Branch: branch, Body: body})
	if err != nil {
		return 0, err
	}
	return s.Requests.Request(ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: workspace, Proposer: proposer, Target: target,
		About: describeRuleset(body) + " no repositório " + repo})
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
			return s.human(ctx, ws, i18n.M("approval.why_owner_declined"))
		}
		return nil
	}
	if s.Workspace != nil {
		want, _, werr := s.Workspace(ctx, ws)
		if werr != nil {
			return fmt.Errorf("%w: the repository of %s could not be read: %v", chain.ErrWrongRepo, ws, werr)
		}
		if !chain.SameRepo(want, t.Repo) {
			return fmt.Errorf("%w: %q is not %q", chain.ErrWrongRepo, t.Repo, want)
		}
	}
	gm, _, err := s.GrandMaster(ctx, ws)
	if err != nil {
		return err
	}
	err = gm.CreateRuleset(ctx, t.Repo, t.Body)
	var api *github.APIError
	switch {
	case err == nil:
		name, _ := t.Body["name"].(string)
		s.tell(ctx, ws, i18n.M("approval.ruleset_created", "name", name, "repo", t.Repo))
		return nil
	case t.PlanB && errors.As(err, &api) && api.Status == http.StatusForbidden:
		return s.human(ctx, ws, i18n.M("approval.why_private_plan", "detail", api.Message))
	case t.PlanB:
		s.tell(ctx, ws, i18n.M("approval.ruleset_not_created", "repo", t.Repo, "reason", err.Error()))
		return err
	}
	return err
}

func (s *Settings) human(ctx context.Context, workspace string, why i18n.Msg) error {
	if err := s.Modes.Set(ctx, workspace, ModeHuman, i18n.Render(i18n.PtBR, why)); err != nil {
		return err
	}
	s.tell(ctx, workspace, i18n.M("approval.human_mode", "workspace", workspace, "why", why))
	return nil
}

func (s *Settings) tell(ctx context.Context, workspace string, m i18n.Msg) {
	if s.Tell != nil {
		s.Tell(ctx, workspace, m)
	}
}
