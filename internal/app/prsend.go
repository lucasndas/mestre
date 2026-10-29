package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// The sending of the branch of a table to GitHub (`mestre pr open|update`) in
// mestre run (docs/github/acoes-por-nivel.md#envio-pelo-espelho): always from the
// mirror of the Citadel, which has the commits of every machine of the table (a
// Tower sends its commits to the mirror at each done), with the token of the App
// of who asks, and a branch that changes the workflows only as the class allows.

// prBranches is github.Branches over the mirror of the workspace.
type prBranches struct{ orc *Orchestration }

func (b prBranches) Prepare(ctx context.Context, tableKey string) (github.Branch, error) {
	ws, id, ok := cutKey(tableKey)
	if !ok {
		return github.Branch{}, github.ErrNoPushSetup
	}
	tb, err := b.orc.Tables.Get(ctx, ws, id)
	if err != nil {
		return github.Branch{}, err
	}
	w, err := b.orc.Workspaces.Get(ctx, ws)
	if err != nil {
		return github.Branch{}, err
	}
	mirror, err := b.orc.Workspaces.OpenMirror(ctx, ws)
	if err != nil {
		return github.Branch{}, err
	}
	head, err := mirror.ResolveRef(ctx, "refs/heads/"+tb.Branch)
	if err != nil {
		return github.Branch{}, fmt.Errorf("the branch of the table could not be read: %w", err)
	}
	return github.Branch{Repo: mirror, Name: tb.Branch, Base: "refs/remotes/origin/" + tb.BaseBranch, BaseBranch: tb.BaseBranch, Head: string(head), Remote: w.CloneURL}, nil
}

// stdPush is github.PushAuth: a token of the installation of the App of who asks,
// limited to the repository and to what the push needs. The permission of the
// workflows is asked only when the branch changes them, and only from an App
// that was made with it.
type stdPush struct{ r *stdRun }

func (p stdPush) PushAuth(ctx context.Context, who github.Asker, repo string, workflows bool) (gitx.Auth, error) {
	orc := p.r.orc.Load()
	if orc == nil || p.r.closed.Load() {
		return gitx.Auth{}, errNoFence
	}
	// the lock may have been confirmed while the change was read
	if p.r.lock.Locked(ctx) {
		return gitx.Auth{}, errors.New("app: the Mestre is locked")
	}
	ch, err := orc.Registry.Get(ctx, who.Character)
	if err != nil {
		return gitx.Auth{}, err
	}
	id, err := stdClients(p).identity(ctx, ch)
	if err != nil {
		return gitx.Auth{}, err
	}
	perms := map[string]string{"contents": "write"}
	if workflows {
		app, err := (&github.Registry{DB: p.r.env.DB}).Get(ctx, id.App)
		if err != nil {
			return gitx.Auth{}, err
		}
		if !app.Workflows {
			return gitx.Auth{}, github.ErrWorkflowPermission
		}
		perms["workflows"] = "write"
	}
	tok, err := p.r.tokens.Installation(ctx, github.Request{App: id.App, Repo: repo, Permissions: perms})
	if err != nil {
		return gitx.Auth{}, err
	}
	web := p.r.s.GitHubWeb
	if web == "" {
		web = DefaultGitHubWeb
	}
	return gitx.BasicAuth(strings.TrimRight(web, "/")+"/", "x-access-token", tok.Secret()), nil
}

// workflowsGate is github.Workflows over the chain of approval: the policy is the
// git.workflows of the effective rules of the character that asks, and a branch
// that changes the workflows under `ask` opens a request envio_workflows bound to
// the repository of the workspace, the branch and the head.
type workflowsGate struct{ orc *Orchestration }

func (g workflowsGate) Policy(ctx context.Context, who github.Asker) (classes.Workflows, error) {
	if g.orc.Chain == nil {
		return classes.WorkflowsNever, errors.New("app: this period has no chain of approval")
	}
	return g.orc.Chain.WorkflowsPolicy(ctx, who.Character)
}

func (g workflowsGate) Approved(ctx context.Context, who github.Asker, branch, head string) (bool, int64, error) {
	if g.orc.Chain == nil {
		return false, 0, errors.New("app: this period has no chain of approval")
	}
	ws, _, ok := cutKey(who.Table)
	if !ok {
		return false, 0, github.ErrNoPushSetup
	}
	repo, err := workspaceRepos(g).Repo(ctx, ws)
	if err != nil {
		return false, 0, err
	}
	return g.orc.Chain.Workflows(ctx, chain.WorkflowsAsk{Requester: who.Character, Repo: repo, Branch: branch, Head: head,
		AboutMsg: i18n.M("approval.about_by_class", "class", who.Class)})
}

// NewWorkflowsGate is the gate of the workflows that mestre run puts in the
// sending of the branches, over the chain of approval of an orchestration.
func NewWorkflowsGate(orc *Orchestration) github.Workflows { return workflowsGate{orc} }

// WorkflowsTarget is the live target of a request envio_workflows: the same
// repository and branch, and the head the branch has in the mirror now, so an
// approval is applied only to the head that was asked about.
func (o *Orchestration) WorkflowsTarget(ctx context.Context, r chain.Request) (json.RawMessage, error) {
	var t chain.WorkflowsTarget
	if err := json.Unmarshal(r.Target, &t); err != nil {
		return nil, err
	}
	tb, err := o.Tables.Get(ctx, r.Workspace, r.Table)
	if err != nil {
		return nil, err
	}
	if tb.Branch != t.Branch {
		return nil, fmt.Errorf("app: the request is for the branch %q and the table works on %q", t.Branch, tb.Branch)
	}
	mirror, err := o.Workspaces.OpenMirror(ctx, r.Workspace)
	if err != nil {
		return nil, err
	}
	head, err := mirror.ResolveRef(ctx, "refs/heads/"+tb.Branch)
	if err != nil {
		return nil, err
	}
	t.Head = string(head)
	return t.JSON(), nil
}
