package decrees

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/users"
)

var _ chain.Grants = (*Service)(nil)

// Exception implements chain.Grants: the one-time exception that an approval
// ("Aprovar agora") makes, tied to the exact action and to the character that
// asked, used up at its first use. Who approves is stored with it, at the level
// he has; an exception of an agent only turns an ask into allow, an exception of
// a person stands over the rules. The request makes at most one.
func (s *Service) Exception(ctx context.Context, e chain.ExceptionGrant) error {
	if e.Character == "" {
		return errors.New("decrees: an exception needs the character that asked")
	}
	now := s.clk.Now()
	level, err := s.levelOfApprover(ctx, s.o.DB.Reader(), e.By, e.Level)
	if err != nil {
		return err
	}
	a := e.Action
	d := Decree{Kind: KindException, State: Active, Effect: Effect{Kind: EffectPermission}, Target: Target{Kind: TargetCharacter, Name: e.Character},
		Workspace: s.workspaceOf(ctx, e.Request), Author: e.By, AuthorLevel: level, Text: fmt.Sprintf("Aprovação do pedido %d", e.Request),
		Origin: Origin{Channel: "approval"}, Request: e.Request, Action: &a, Until: now.Add(s.ttl)}
	if d.Workspace == "" {
		return errors.New("decrees: the workspace of the request is not known")
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := insertDecree(ctx, tx, d, fmt.Sprintf("aprovação do pedido %d", e.Request), now)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return nil // the same approval, applied twice
		}
		return err
	})
}

// workspaceOf is the workspace of the request that makes a decree ("" when the
// requests are not known).
func (s *Service) workspaceOf(ctx context.Context, request int64) string {
	if s.o.Requests == nil {
		return ""
	}
	r, err := s.o.Requests.Get(ctx, request)
	if err != nil {
		return ""
	}
	return r.Workspace
}

// levelOfApprover is the level of who approved: a person at the level he has
// now (nobody who is not active or is below an approver makes a decree), an
// agent at the level of the chain.
func (s *Service) levelOfApprover(ctx context.Context, q queryer, by string, l chain.Level) (classes.Level, error) {
	switch l.Kind {
	case chain.LevelGrandMaster:
		return classes.LevelGrandMaster, nil
	case chain.LevelMesa, chain.LevelAgent:
		return classes.LevelTableMaster, nil
	}
	u, err := readUser(ctx, q, by)
	if err != nil {
		return "", fmt.Errorf("decrees: the approver %s: %w", by, err)
	}
	if u.State != users.Active || users.Require(u.Level, users.OneOffException) != nil {
		return "", fmt.Errorf("decrees: %s does not make decrees", u.Name)
	}
	return classLevel(u.Level), nil
}

// Decree implements chain.Grants: what only people give. The answers
// "aprovar por um tempo", "o padrão", "sempre para a classe", "negar sempre" make
// a permanent rule (with validity, or none) for the class of whoever asked;
// "delegar", a delegation of the Owner. The level of who answered is read
// again here: below an Administrator no rule is made; a delegation is only the
// Owner's. The PIN of these answers is asked by the flow of approval before it
// reaches here, and an answer that only denies asks none.
func (s *Service) Decree(ctx context.Context, g chain.DecreeGrant) error {
	r, err := s.request(ctx, g.Request)
	if err != nil {
		return err
	}
	p, err := s.proposalOf(r, g)
	if err != nil {
		return err
	}
	var d Decree
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		u, err := readUser(ctx, tx, g.By)
		if err != nil {
			return err
		}
		if !p.Until.IsZero() && !p.Until.After(now) {
			return refuse(CodeInvalid, "a validade já passou")
		}
		why, ref := check(p, u, now)
		if ref != nil {
			return ref
		}
		d = s.build(p, u, why != "", now)
		d.Request = g.Request
		_, err = insertDecree(ctx, tx, d, fmt.Sprintf("resposta ao pedido %d: %s", g.Request, g.Answer), now)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return nil
		}
		if err != nil {
			return err
		}
		return nil
	})
	return err
}

func (s *Service) request(ctx context.Context, id int64) (chain.Request, error) {
	if s.o.Requests == nil {
		return chain.Request{}, errors.New("decrees: the requests are not known")
	}
	return s.o.Requests.Get(ctx, id)
}

// proposalOf turns the answer of a person into the proposal the checks apply to.
func (s *Service) proposalOf(r chain.Request, g chain.DecreeGrant) (Proposal, error) {
	p := Proposal{Author: g.By, Kind: KindRule, Workspace: r.Workspace, Mesa: g.Table, Origin: Origin{Channel: "approval"},
		Text: fmt.Sprintf("Resposta %q ao pedido %d: %s", g.Answer, r.ID, r.About), Target: Target{Kind: TargetClass, Name: g.Class}}
	if g.Class == "" && g.Answer != chain.AnswerDelegate {
		return Proposal{}, errors.New("decrees: the request has no class to make a rule for")
	}
	switch g.Answer {
	case chain.AnswerApproveFor, chain.AnswerApprovePattern, chain.AnswerApproveAlways:
		grant, err := grantOf(r, g.Pattern)
		if err != nil {
			return Proposal{}, err
		}
		p.Effect = Effect{Kind: EffectPermission, Allow: grant}
		if g.For > 0 {
			p.Until = s.clk.Now().Add(g.For)
		}
	case chain.AnswerDenyAlways:
		grant, err := grantOf(r, g.Pattern)
		if err != nil {
			return Proposal{}, err
		}
		p.Effect = Effect{Kind: EffectPermission, Deny: grant}
	case chain.AnswerDelegate:
		if r.Risk.Risk == classes.RiskMedium {
			// The Grand Master already decides a medio inside the policy: whoever
			// wants it looser makes a decree of policy, for an Administrator or
			// above (docs/aprovacoes/delegacao.md#delegação-de-decisão).
			p.Effect = Effect{Kind: EffectPolicy, Setting: string(r.Type), Loosens: true}
			p.Target = Target{Kind: TargetWorkspace, Name: r.Workspace}
			p.Mesa = r.Table
			if g.For > 0 {
				p.Until = s.clk.Now().Add(g.For)
			}
			break
		}
		p.Effect = Effect{Kind: EffectDelegation, Setting: string(r.Type)}
		p.Target = Target{Kind: TargetWorkspace, Name: r.Workspace}
		p.Mesa = r.Table
		if g.For > 0 {
			p.Until = s.clk.Now().Add(g.For)
		}
	default:
		return Proposal{}, fmt.Errorf("decrees: the answer %q makes no decree", g.Answer)
	}
	return p, nil
}

// grantOf is what a rule of the answer frees or forbids: the pattern, or the
// exact action of the request.
func grantOf(r chain.Request, pattern string) (*classes.Grant, error) {
	pattern = strings.TrimSpace(pattern)
	a := r.Action
	switch {
	case pattern != "" && a != nil:
		switch a.Kind {
		case providers.KindShell:
			return &classes.Grant{Commands: []string{pattern}}, nil
		case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
			return &classes.Grant{Files: []string{pattern}}, nil
		case providers.KindWeb:
			return &classes.Grant{Domains: []string{pattern}}, nil
		}
		return &classes.Grant{Tools: []string{pattern}}, nil
	case a == nil:
		return nil, errors.New("decrees: a rule is for the request of an action")
	}
	switch a.Kind {
	case providers.KindShell:
		return &classes.Grant{Commands: []string{a.Command}}, nil
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		return &classes.Grant{Files: append([]string(nil), a.Paths...)}, nil
	case providers.KindWeb:
		if h := hostOf(a.URL); h != "" {
			return &classes.Grant{Domains: []string{h}}, nil
		}
	}
	if a.Tool == "" {
		return nil, errors.New("decrees: the action has nothing to make a rule of")
	}
	return &classes.Grant{Tools: []string{a.Tool}}, nil
}
