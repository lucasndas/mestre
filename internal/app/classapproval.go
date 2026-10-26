package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

// The approval of a class of the workspace through the flow of approval
// (docs/aprovacoes/fluxo.md, docs/classes/classes.md#onde-a-classe-é-alterada):
// a class that the Grand Master proposes waits as a proposal, and the engine
// puts a request of the type classe_workspace in front of the people, bound to
// the hash of the package. The Grand Master, who proposed it, decides nothing
// of that request (chain: the Proposer); the "Aprovar" of an Administrator or
// above is what puts the class in use (Library.Approve, which checks the hash
// and the ceiling again). A change found on disk is approved in the panel.

// classTarget is what the request is bound to, read back from it when the
// people answer.
type classTarget struct {
	Proposal int64  `json:"proposal"`
	Hash     string `json:"hash"`
	Class    string `json:"class"`
}

// classApprovals is the classes.ClassAlerts of the libraries and the
// chain.Sink of the type classe_workspace.
type classApprovals struct {
	chain *atomic.Pointer[chain.Service]
	books func(workspace string) *classes.Library
	users func() *users.Service
	tell  func(ctx context.Context, text string)
}

var _ chain.Sink = (*classApprovals)(nil)

// alertsFor is the ClassAlerts of the library of a workspace.
func (c *classApprovals) alertsFor(workspace string) classes.ClassAlerts {
	return classAlerts{c: c, workspace: workspace}
}

type classAlerts struct {
	c         *classApprovals
	workspace string
}

// ClassChanged opens the request of a proposal the Mestre made for a class of
// the workspace. The other alerts have no request: the panel shows them.
func (a classAlerts) ClassChanged(ctx context.Context, al classes.ClassAlert) error {
	if al.Kind != classes.ClassWaiting || al.Key.Scope != classes.ClassWorkspace || al.Proposal == 0 {
		return nil
	}
	svc := a.c.chain.Load()
	lib := a.c.books(a.workspace)
	if svc == nil || lib == nil {
		return errors.New("app: this period has no chain of approval to ask the people about the class")
	}
	p, ok, err := lib.Records.Proposal(ctx, a.workspace, al.Proposal)
	if err != nil || !ok {
		return fmt.Errorf("app: the proposal %d of the class %s: %v", al.Proposal, al.Key.Name, err)
	}
	target, err := json.Marshal(classTarget{Proposal: p.ID, Hash: p.Hash, Class: al.Key.Name})
	if err != nil {
		return err
	}
	_, err = svc.Request(ctx, chain.OpenRequest{Type: classes.RequestWorkspaceClass, Workspace: a.workspace, Proposer: p.By, Target: target,
		About: fmt.Sprintf("criar ou mudar a classe %s do workspace (proposta %d, pacote %s)", al.Key.Name, p.ID, shortHash(p.Hash))})
	if err != nil {
		return fmt.Errorf("app: asking the people about the class %s: %w", al.Key.Name, err)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Decided puts the class in use when a person of the level approved it. A
// denial leaves the proposal as it is, pending, for whoever proposed it to
// change; the request being over is what the people answered.
func (c *classApprovals) Decided(ctx context.Context, res chain.Result) error {
	r := res.Request
	if r.Type != classes.RequestWorkspaceClass || r.State != chain.Approved {
		return nil
	}
	// only an "Aprovar" puts the class in use; delegating the request is not approving the package
	if r.Decision == nil || !r.Decision.Answer.Approves() {
		c.tell(ctx, fmt.Sprintf("O pedido %d, da classe do workspace, foi encerrado sem um \"Aprovar\": a classe não entrou em uso.", r.ID))
		return nil
	}
	by, user := "", false
	if r.Decision != nil {
		by, user = strings.CutPrefix(r.Decision.By, "user:")
	}
	var t classTarget
	if err := json.Unmarshal(r.Target, &t); err != nil || t.Proposal == 0 || t.Hash == "" {
		return fmt.Errorf("app: the request %d names no proposal of a class", r.ID)
	}
	lib := c.books(r.Workspace)
	if !user || by == "" || lib == nil || c.users == nil || c.users() == nil {
		// no person approved it (an agent never does): nothing is put in use
		c.tell(ctx, fmt.Sprintf("O pedido %d, da classe %s, foi aprovado sem uma pessoa: a classe não entrou em uso.", r.ID, t.Class))
		return fmt.Errorf("app: the class of the request %d was approved by no user", r.ID)
	}
	u, err := c.users().Get(ctx, by)
	if err != nil {
		return err
	}
	if err := lib.Approve(ctx, t.Proposal, t.Hash, classes.Author{Level: classLevelOf(u.Level), ID: u.ID}); err != nil {
		c.tell(ctx, fmt.Sprintf("A aprovação da classe %s (pedido %d) não pôde ser aplicada: %v. Veja a proposta no painel.", t.Class, r.ID, err))
		return err
	}
	return nil
}

// classLevelOf is the level of a user as the library of classes names it.
func classLevelOf(l users.Level) classes.Level {
	switch l {
	case users.Owner:
		return classes.LevelOwner
	case users.Administrator:
		return classes.LevelAdmin
	case users.Approver:
		return classes.LevelApprover
	}
	return ""
}
