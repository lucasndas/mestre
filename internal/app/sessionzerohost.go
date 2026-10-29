package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/verification"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/telegram"
)

// The session zero of the workspace (docs/sessao-zero/workspace.md) in the
// Citadel: the stages apps, files, analysis and approval_test are registered on
// the workspaces. What needs a real agent (the analysis of the Grand Master) or a
// choice of the panel (the answer about versioning .mestre/) is not faked: it stops
// with its reason.

// pullPushPermissions are the permissions of the token that sends the branch of
// the session zero and opens its pull request.
var pullPushPermissions = map[string]string{"metadata": "read", "contents": "write", "pull_requests": "write"}

// Errors the session zero answers for what this version does not do.
var (
	// errAnalysisNeedsAgent is the stage analysis: the Grand Master analyses the
	// project as an agent, and the milestone that runs the agent in the stage is
	// not this one.
	errAnalysisNeedsAgent = errors.New("the analysis of the project is made by the Grand Master as an agent, which this version does not run in the stage yet (docs/desenvolvimento/ligacao-de-producao.md)")
	// errNoGrandMaster: the Grand Master is made with the workspace, with the sheet
	// chosen in the panel (milestone 6); the stage files only starts one that exists.
	errNoGrandMaster = errors.New("the workspace has no sheet chosen for its Grand Master: the panel chooses it when the workspace is created")
)

// SessionZero composes the session zero of the workspaces over the orchestration:
// its stages are registered on orc.Workspaces. worktree is where the files of the
// providers come from (the same the tables use). The Analyst refuses (see
// errAnalysisNeedsAgent), so a workspace stops at the stage analysis with that
// reason, in the record of the stage.
func (r *stdRun) SessionZero(orc *Orchestration, worktree sessionzero.Worktree) (*sessionzero.Service, error) {
	if orc == nil || orc.Workspaces == nil {
		return nil, errors.New("app: the session zero needs the workspaces")
	}
	svc, err := sessionzero.New(sessionzero.Options{
		Workspaces: orc.Workspaces, DB: r.env.DB, Clock: r.env.Clock,
		Installations: r.Installations(), AppReview: r.AppReview(),
		Users: szUsers{r}, Approvals: szApprovals{r: r, orc: orc}, Checklist: szChecklist{log: r.env.Log},
		GrandMaster: szGrandMaster{r: r, orc: orc}, Analyst: r.analystOr(szAnalyst{}), Verification: r.verificationKeeper(), PullRequests: szPullRequests{r: r, orc: orc},
		Worktree: worktree, Masker: r.env.Masker, Log: r.env.Log,
	})
	if err != nil {
		return nil, err
	}
	r.zero.Store(svc)
	return svc, nil
}

// analystOr is the analyst of the stage analysis: the one a test put, or def.
func (r *stdRun) analystOr(def sessionzero.Analyst) sessionzero.Analyst {
	if r.analyst != nil {
		return r.analyst
	}
	return def
}

// verificationKeeper keeps the commands the analysis proposes in verificacao.yaml and tells the
// Administrators they wait for the confirmation.
func (r *stdRun) verificationKeeper() verification.Keeper {
	return verification.Keeper{Log: r.env.Log, Notify: func(ctx context.Context, workspace string, msg i18n.Msg) error {
		_, err := r.alert.AdminAlert(ctx, workspace, msg)
		if errors.Is(err, telegram.ErrQueued) {
			return nil
		}
		return err
	}}
}

// sessionZeroHost is what an IntegrationRun may also be: the one that composes
// the session zero of the workspaces.
type sessionZeroHost interface {
	SessionZero(orc *Orchestration, worktree sessionzero.Worktree) (*sessionzero.Service, error)
}

// szUsers is sessionzero.Users over the bot.
type szUsers struct{ r *stdRun }

var _ sessionzero.Users = szUsers{}

// AskOwnerToInstall sends the links to the Owner alone (docs/github/criacao-de-apps.md).
func (u szUsers) AskOwnerToInstall(ctx context.Context, workspace string, links []sessionzero.InstallLink) error {
	parts := []i18n.Msg{i18n.M("workspace.install_apps", "workspace", workspace)}
	for _, l := range links {
		parts = append(parts, i18n.M("workspace.install_link", "app", l.App, "url", l.URL))
	}
	u.r.alert.OwnerAlert(ctx, i18n.Concat(parts...))
	return nil
}

// Notify tells the Owner and the Administrators who act in the workspace.
func (u szUsers) Notify(ctx context.Context, workspace string, n sessionzero.Notice) error {
	return u.tell(ctx, workspace, n.Msg)
}

// AskVersioning tells the Administrators the question is pending. The answer
// (versionar ou ficar só local) is given in the panel (milestone 6): the bot
// does not take it.
func (u szUsers) AskVersioning(ctx context.Context, workspace string) error {
	return u.tell(ctx, workspace, i18n.M("workspace.versioning_pending", "workspace", workspace))
}

func (u szUsers) tell(ctx context.Context, workspace string, m i18n.Msg) error {
	_, err := u.r.alert.AdminAlert(ctx, workspace, m)
	if errors.Is(err, telegram.ErrQueued) {
		return nil // it goes out when the bot is up
	}
	return err
}

// szApprovals is sessionzero.Approvals over the chain of approval.
type szApprovals struct {
	r   *stdRun
	orc *Orchestration
}

var _ sessionzero.Approvals = szApprovals{}

// RequestAgentsMD opens the request proposta_agents_md for the people to answer.
// The request is short (About); the text is in the agent block (Task) and the
// request is bound to the exact text (Target: the workspace and the SHA-256 of
// the proposed AGENTS.md), so what the people approve is what is sent: the text
// that goes into the pull request must still be the one with that hash
// (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato). The answer comes back by
// szDecisions.
func (a szApprovals) RequestAgentsMD(ctx context.Context, workspace, text string) (string, error) {
	if a.orc.Chain == nil {
		return "", errors.New("app: this period has no chain of approval for the AGENTS.md proposal")
	}
	id, err := a.orc.Chain.Request(ctx, chain.OpenRequest{Type: classes.RequestAgentsMD, Workspace: workspace,
		About: "Proposta de AGENTS.md para o projeto " + workspace + " (o texto inteiro está no bloco do agente)", Task: text, Target: agentsMDTarget(workspace, text)})
	if err != nil {
		return "", err
	}
	return fmt.Sprint(id), nil
}

// agentsMDTarget is the exact target of an AGENTS.md proposal: the workspace and
// the SHA-256 of the text.
func agentsMDTarget(workspace, text string) json.RawMessage {
	sum := sha256.Sum256([]byte(text))
	b, _ := json.Marshal(map[string]string{"workspace": workspace, "sha256": hex.EncodeToString(sum[:])})
	return b
}

// szDecisions is the owner of the type proposta_agents_md in the chain: what the
// people (or the chain) decided goes to the session zero. Only the request the
// session zero itself recorded counts.
type szDecisions struct{ r *stdRun }

var _ chain.Sink = szDecisions{}

func (d szDecisions) Decided(ctx context.Context, res chain.Result) error {
	rq := res.Request
	if rq.Type != classes.RequestAgentsMD || (rq.State != chain.Approved && rq.State != chain.Denied) {
		return nil // expired, cancelled or invalidated: the proposal waits for another try
	}
	svc := d.r.zero.Load()
	if svc == nil {
		return errors.New("app: the session zero is not composed in this period")
	}
	st, err := svc.State(ctx, rq.Workspace)
	if err != nil {
		return err
	}
	if st.AgentsMDRequest != fmt.Sprint(rq.ID) {
		return fmt.Errorf("app: request %d is not the AGENTS.md proposal of workspace %s", rq.ID, rq.Workspace)
	}
	if rq.State == chain.Approved && !bytes.Equal(rq.Target, agentsMDTarget(rq.Workspace, st.AgentsMDText)) {
		// the text that would go into the pull request is not the one the people
		// approved: nothing is sent
		return fmt.Errorf("app: the AGENTS.md of workspace %s is not the text request %d approved", rq.Workspace, rq.ID)
	}
	return svc.AgentsMDDecided(ctx, rq.Workspace, rq.State == chain.Approved)
}

// szChecklist is sessionzero.Checklist before the checklist exists (milestone 7):
// the item is only logged.
type szChecklist struct{ log *slog.Logger }

func (c szChecklist) Raise(_ context.Context, item sessionzero.ChecklistItem) error {
	c.log.Warn("app: a checklist item was raised and the checklist is not shown yet", "item", item.ID, "workspace", item.Workspace, "text", item.Text)
	return nil
}

// szGrandMaster is sessionzero.GrandMaster: the stage files starts the Grand Master
// of the workspace with the sheet the panel chose when the workspace was created
// (docs/painel/administracao.md). One that is already alive is left alone.
type szGrandMaster struct {
	r   *stdRun
	orc *Orchestration
}

func (g szGrandMaster) Start(ctx context.Context, workspace string) error {
	if _, ok, err := g.orc.Registry.GrandMaster(ctx, workspace); err != nil || ok {
		return err
	}
	zero := g.r.zero.Load()
	if zero == nil {
		return errNoGrandMaster
	}
	st, err := zero.State(ctx, workspace)
	if err != nil {
		return err
	}
	if st.GrandMasterSheet == "" {
		return errNoGrandMaster
	}
	_, err = g.orc.Characters.Create(ctx, characters.CreateRequest{Sheet: st.GrandMasterSheet, Workspace: workspace, Role: characters.RoleGrandMaster,
		Machine: g.r.env.Machine, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		return fmt.Errorf("the Grand Master with the sheet %s: %w", st.GrandMasterSheet, err)
	}
	return nil
}

// szAnalyst is sessionzero.Analyst without the agent: it refuses, saying why.
type szAnalyst struct{}

func (szAnalyst) Analyze(context.Context, sessionzero.AnalysisInput) (sessionzero.Analysis, error) {
	return sessionzero.Analysis{}, errAnalysisNeedsAgent
}

// szPullRequests is sessionzero.PullRequests with the App "Grão-Mestre": it sends
// the branch the engine built in the mirror and opens the pull request.
type szPullRequests struct {
	r   *stdRun
	orc *Orchestration
	// head reads the commit a branch has on GitHub now (nil: the API of the
	// App); the lease of the push expects exactly it.
	head func(ctx context.Context, repo, branch string) (string, error)
}

var _ sessionzero.PullRequests = szPullRequests{}

// Author is the identity of the App "Grão-Mestre".
func (p szPullRequests) Author(ctx context.Context) (gitx.Signature, error) {
	id, err := github.Identities{Apps: &github.Registry{DB: p.r.env.DB}}.For(ctx, github.Who{Role: github.RoleGrandMaster})
	if err != nil {
		return gitx.Signature{}, fmt.Errorf("the App Grão-Mestre: %w", err)
	}
	return gitx.Signature{Name: id.Name, Email: id.Email, When: p.r.env.Clock.Now()}, nil
}

// Publish pushes the branch with a token of the installation, limited to this
// repository, and opens the pull request unless one is open for the branch.
func (p szPullRequests) Publish(ctx context.Context, pr sessionzero.PullRequest) (string, error) {
	if err := (stdLease{p.r}).Allow(); err != nil {
		return "", err
	}
	w, err := p.orc.Workspaces.Get(ctx, pr.Workspace)
	if err != nil {
		return "", err
	}
	repo := pr.Repo.Owner + "/" + pr.Repo.Name
	api := p.r.gmRest(p.r.gmApp(ctx), pullPushPermissions)
	token, err := api.Token(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("the token of the App Grão-Mestre for %s: %w", repo, err)
	}
	// the fence is checked again once the token exists (it was asked from GitHub,
	// which takes time) and again right before the push
	if err := (stdLease{p.r}).Allow(); err != nil {
		return "", err
	}
	mirror, err := p.orc.Workspaces.OpenMirror(ctx, pr.Workspace)
	if err != nil {
		return "", err
	}
	commit, err := mirror.ResolveRef(ctx, "refs/heads/"+pr.Branch)
	if err != nil {
		return "", fmt.Errorf("the branch %s: %w", pr.Branch, err)
	}
	// The branch is rebuilt from the main branch each time, so the push replaces
	// it, but only the one this read saw: with a lease on the commit the remote has
	// now, whoever pushed to the branch since is never overwritten.
	head := p.head
	if head == nil {
		head = api.RefSHA
	}
	expected, err := head(ctx, repo, pr.Branch)
	switch {
	case errors.Is(err, github.ErrNotFound):
		expected = ""
	case err != nil:
		return "", fmt.Errorf("the branch %s of %s: %w", pr.Branch, repo, err)
	}
	// the credential is for the web of the configured GitHub, never a fixed host
	if err := (stdLease{p.r}).Allow(); err != nil {
		return "", err
	}
	if err := mirror.PushWithLease(ctx, gitx.BasicAuth(p.r.gitWeb()+"/", "x-access-token", token), w.CloneURL, pr.Branch, string(commit), expected); err != nil {
		return "", err
	}
	if open, ok, err := api.OpenPullOf(ctx, repo, pr.Branch, pr.Base); err != nil {
		return "", err
	} else if ok {
		return open.URL, nil
	}
	d, err := api.CreatePull(ctx, repo, pr.Title, pr.Body, pr.Branch, pr.Base, false)
	if err != nil {
		return "", err
	}
	return d.URL, nil
}

// gitWeb is the web base of the configured GitHub, which the credential of a push
// is for (never a fixed host: a GitHub of another address would not be reached
// with the token, and the token would go to a host that is not the configured).
func (r *stdRun) gitWeb() string {
	if web := strings.TrimRight(r.s.GitHubWeb, "/"); web != "" {
		return web
	}
	return DefaultGitHubWeb
}
