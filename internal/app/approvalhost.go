package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/ghcli"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The approval on GitHub (docs/github/aprovacao-no-github.md) over the Apps: the
// facts of the PRs for the conditions of the policy, the Approver that applies
// the approvals of people, the settings of the repository and the reviews made
// on GitHub itself. Each token is limited to the permissions of its use.
var (
	// prReadPermissions read a PR, its files, labels, checks and reviews.
	prReadPermissions = map[string]string{"metadata": "read", "issues": "read", "pull_requests": "read", "checks": "read", "statuses": "read"}
	// reviewPermissions send the review of the Grand Master.
	reviewPermissions = map[string]string{"metadata": "read", "pull_requests": "write"}
	// ownersPermissions read the rules, the file of owners and the files of a PR.
	ownersPermissions = map[string]string{"metadata": "read", "administration": "read", "contents": "read", "pull_requests": "read"}
	// settingsPermissions read the rules and create a ruleset.
	settingsPermissions = map[string]string{"metadata": "read", "administration": "write"}
	// The approval test: "Mestre" opens and closes the pull request, "Grão-Mestre" reads the rules and reviews it.
	probeMestrePermissions = map[string]string{"metadata": "read", "contents": "write", "pull_requests": "write"}
	probeGMPermissions     = map[string]string{"metadata": "read", "administration": "read", "pull_requests": "write"}
)

// soft turns a nil pointer of a sink into a nil sink (a typed nil is not nil).
func soft[T interface {
	Decided(context.Context, chain.Result) error
}](p T) chain.Sink {
	var zero T
	if any(p) == any(zero) {
		return nil
	}
	return p
}

var errNoApproval = errors.New("app: the approval on GitHub is not composed in this period")

// lateSink gives the chain a sink made after it (the approvers need the chain).
// An approval of people that could not be applied on GitHub is told to the
// Owner: the people saw "approved", and nothing went out.
//
// What the sink does on GitHub (a review, a merge, a ruleset) starts only under
// the lease of the epoch (docs/guilda/trava-no-github.md#aluguel): with the lease
// gone it waits in the queue of the fence and goes out once the epoch is
// confirmed. The sink re-reads the head it approves, so a late run is safe.
type lateSink struct {
	get   func() chain.Sink
	alert func(ctx context.Context, text string)
	// lease runs fn under the lease or keeps it for when the lease is back
	// (queued); nil runs it now.
	lease func(ctx context.Context, name string, fn func(context.Context) error) (queued bool, err error)
}

func (l lateSink) Decided(ctx context.Context, res chain.Result) error {
	s := l.get()
	if s == nil {
		return errNoApproval
	}
	apply := func(ctx context.Context) error {
		err := s.Decided(ctx, res)
		if err != nil && res.Approved && l.alert != nil {
			var ref *chain.Refusal
			why := "veja o log do motor"
			if errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired {
				why = i18n.Render(i18n.PtBR, ref.Msg)
			}
			l.alert(ctx, fmt.Sprintf("A aprovação do pedido #%d foi dada, mas não foi aplicada no GitHub: %s.", res.Request.ID, why))
		}
		return err
	}
	if l.lease == nil {
		return apply(ctx)
	}
	_, err := l.lease(ctx, fmt.Sprintf("approval:%d", res.Request.ID), apply)
	return err
}

// isolated keeps the failure of a sink from holding the reading of the events:
// the cursor does not move while a sink answers an error, and every event of the
// workspace would be read again at each cycle. The failure is logged.
type isolated struct {
	inner github.Sink
	log   *slog.Logger
	name  string
}

func (i isolated) Deliver(ctx context.Context, w github.Watch, table string, evs []github.Event) error {
	if err := i.inner.Deliver(ctx, w, table, evs); err != nil {
		i.log.Warn("app: a reader of the events of GitHub failed", "reader", i.name, "workspace", w.Workspace, "err", err)
	}
	return nil
}

// botLogins are the logins of the bots of the Apps of the Mestre.
func (r *stdRun) botLogins(ctx context.Context) (map[string]bool, error) {
	all, err := (&github.Registry{DB: r.env.DB}).List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, a := range all {
		// the Grão-Mestre is who the condition watches, not another character
		if a.Slug != "" && a.Role != github.RoleGrandMaster {
			out[a.BotLogin()] = true
		}
	}
	return out, nil
}

// source reads a PR for the conditions of the policy and the live target of a
// request (approvals.GitHubSource).
func (r *stdRun) source() approvals.GitHubSource {
	return approvals.GitHubSource{
		Reader: func(ctx context.Context, _ string) (approvals.PullReader, error) {
			return r.gmRest(r.gmApp(ctx), prReadPermissions), nil
		},
		Characters: r.botLogins,
		GrandMasterLogin: func(ctx context.Context) (string, error) {
			a, err := (&github.Registry{DB: r.env.DB}).Fixed(ctx, github.RoleGrandMaster)
			if err != nil || a.Slug == "" {
				return "", errors.New("the App Grão-Mestre is not made yet")
			}
			return a.BotLogin(), nil
		},
		BaseTip: func(ctx context.Context, workspace, table string) (string, error) {
			orc := r.orc.Load()
			if orc == nil {
				return "", errNoApproval
			}
			t, err := orc.Tables.Get(ctx, workspace, table)
			if err != nil {
				return "", err
			}
			mirror, err := orc.Workspaces.OpenMirror(ctx, workspace)
			if err != nil {
				return "", err
			}
			tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
			return string(tip), err
		},
	}
}

// ownersFor reads who the repository requires to review a PR (github.CodeOwners).
type ownersFor struct{ r *stdRun }

func (o ownersFor) Required(ctx context.Context, _, repo string, n int) ([]string, error) {
	return github.CodeOwners{Client: o.r.gmRest(o.r.gmApp(ctx), ownersPermissions)}.Required(ctx, repo, n)
}

// decreeDelegations tells the Approver of the decrees of delegation.
type decreeDelegations struct{ d *decrees.Service }

func (a decreeDelegations) Delegation(ctx context.Context, req chain.Request) (string, bool, error) {
	d, ok, err := a.d.Delegation(ctx, req)
	return d.Author, ok, err
}

func (a decreeDelegations) Used(ctx context.Context, req chain.Request, by string) error {
	return a.d.Used(ctx, req, by)
}

// tellUser says something to a user by the bot.
type tellUser struct{ r *stdRun }

func (t tellUser) Tell(ctx context.Context, userID, text string) error {
	t.r.alert.Tell(ctx, userID, text)
	return nil
}

// grandMasterReview gives the client of the App "Grão-Mestre" for the review and
// the login of its bot.
func (r *stdRun) grandMasterReview(ctx context.Context, _ string) (approvals.ReviewClient, string, error) {
	a, err := (&github.Registry{DB: r.env.DB}).Fixed(ctx, github.RoleGrandMaster)
	if err != nil {
		return nil, "", fmt.Errorf("the App Grão-Mestre: %w", err)
	}
	if !a.Ready() || a.Slug == "" {
		return nil, "", fmt.Errorf("%w: %s", github.ErrAppNotReady, a.Name)
	}
	return r.gmRest(a.Name, reviewPermissions), a.BotLogin(), nil
}

// approvalSetup makes the approvers over the chain of the orchestration.
func (r *stdRun) approvalSetup(orc *Orchestration) {
	if orc.Chain == nil {
		return
	}
	modes := &approvals.Modes{DB: r.env.DB, Clock: r.env.Clock}
	r.approver.Store(&approvals.Approver{
		Approved: &chain.Store{DB: r.env.DB}, Opener: orc.Chain, GrandMaster: r.grandMasterReview,
		Personal: approvals.PersonalCredentials{Source: r.personal}, Required: ownersFor{r}, Delegations: decreeDelegations{r.decrees},
		Users: r.users, Modes: modes, Tell: tellUser{r}, Logger: r.env.Log,
		Repo: func(ctx context.Context, workspace string) (string, error) {
			return workspaceRepoOf{orc.Workspaces}.Repo(ctx, workspace)
		},
	})
	r.settings.Store(&approvals.Settings{
		Requests: orc.Chain, Modes: modes,
		Workspace: func(ctx context.Context, workspace string) (string, string, error) {
			w, err := orc.Workspaces.Get(ctx, workspace)
			if err != nil {
				return "", "", err
			}
			return w.Repo.String(), w.DefaultBranch, nil
		},
		GrandMaster: func(ctx context.Context, _ string) (approvals.SettingsClient, int64, error) {
			a, err := (&github.Registry{DB: r.env.DB}).Fixed(ctx, github.RoleGrandMaster)
			if err != nil {
				return nil, 0, fmt.Errorf("the App Grão-Mestre: %w", err)
			}
			return r.gmRest(a.Name, settingsPermissions), a.AppID, nil
		},
		Tell:   func(ctx context.Context, _, text string) { r.alert.OwnerAlert(ctx, text) },
		Logger: r.env.Log,
	})
}

// reviews takes the reviews made on GitHub itself as answers of the people.
func (r *stdRun) reviews() *approvals.GitHubReviews {
	return &approvals.GitHubReviews{
		DB: r.env.DB, Clock: r.env.Clock, Requests: &chain.Store{DB: r.env.DB}, Flow: r.flow, Users: r.users, PIN: r.ac, Logger: r.env.Log,
		Reader: func(ctx context.Context, _ string) (approvals.ReviewReader, error) {
			return r.gmRest(r.gmApp(ctx), prReadPermissions), nil
		},
	}
}

// AppReview is the approval test of the session zero over the Apps: "Mestre"
// opens the pull request and "Grão-Mestre" reviews it.
func (r *stdRun) AppReview() sessionzero.AppReview {
	return sessionzero.GitHubReview{Probe: github.ReviewProbe{
		Mestre:      r.roleRest(github.RoleMestre, probeMestrePermissions),
		GrandMaster: r.roleRest(github.RoleGrandMaster, probeGMPermissions),
	}}
}

// mergePermissions are the permissions of the token of a merge: the merge is a write
// in the contents of the repository and in its pull request, and nothing else.
var mergePermissions = map[string]string{"metadata": "read", "contents": "write", "pull_requests": "write"}

// lateApprover gives `gh approve` the Approver the period makes after the handlers.
type lateApprover struct{ r *stdRun }

func (l lateApprover) Approve(ctx context.Context, o approvals.ApproveOrder) (approvals.Approval, error) {
	a := l.r.approver.Load()
	if a == nil {
		return approvals.Approval{}, errNoApproval
	}
	return a.Approve(ctx, o)
}

// lateRulesets gives `gh settings apply` the Settings the period makes after the handlers.
type lateRulesets struct{ r *stdRun }

func (l lateRulesets) ProposeRuleset(ctx context.Context, workspace, proposer string, body map[string]any) (int64, error) {
	s := l.r.settings.Load()
	if s == nil {
		return 0, errNoApproval
	}
	return s.ProposeRuleset(ctx, workspace, proposer, body)
}

// mergeHost is `gh merge` over the App "Grão-Mestre" and the chain of approval.
type mergeHost struct{ r *stdRun }

var _ ghcli.Merging = mergeHost{}

// Merger gives the REST of the Grão-Mestre with the token of a merge.
func (m mergeHost) Merger(ctx context.Context, _ string) (ghcli.PullMerger, error) {
	app := m.r.gmApp(ctx)
	if app == "" {
		return nil, errors.New("the App Grão-Mestre is not made yet")
	}
	return m.r.gmRest(app, mergePermissions), nil
}

// Standing tells the merge requests of the PR at the commit and the base: the newest
// approved one that still stands (its target has the same base branch and, when the gate
// ran, the tip of the base it ran on is still the tip), and the newest open one.
func (m mergeHost) Standing(ctx context.Context, workspace, repo string, n int, sha, base string) (approved, open int64, err error) {
	st := &chain.Store{DB: m.r.env.DB}
	rs, err := st.ApprovedPR(ctx, workspace, repo, n, sha)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range rs {
		if r.Type != classes.RequestMerge || r.ID <= approved || !m.sameBase(ctx, r, base) {
			continue
		}
		approved = r.ID
	}
	ops, err := st.OpenPR(ctx, workspace, repo, n)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range ops {
		var t struct {
			Head string `json:"head"`
			Base string `json:"base"`
		}
		if r.Type == classes.RequestMerge && r.ID > open && json.Unmarshal(r.Target, &t) == nil && t.Head == sha && t.Base == base {
			open = r.ID
		}
	}
	return approved, open, nil
}

// sameBase says an approved request was for the base the PR goes into now, and that the
// base did not move since the gate ran on it (an approval of a merge into another branch,
// or into a base that was never verified, does not carry over).
func (m mergeHost) sameBase(ctx context.Context, r chain.Request, base string) bool {
	var t struct {
		Base string `json:"base"`
	}
	if json.Unmarshal(r.Target, &t) != nil || t.Base != base {
		return false
	}
	sha := chain.BaseSHAOf(r.Target)
	if sha == "" {
		return true // the repository asks the branch to be up to date: the base moving changes nothing
	}
	tip, err := m.r.source().BaseTip(ctx, r.Workspace, r.Table)
	return err == nil && tip == sha
}

// Redeem spends the exception of an Approver that approves exactly this merge (the
// repository, the PR and the head of the target); see ghcli.Merging.
func (m mergeHost) Redeem(ctx context.Context, workspace string, target json.RawMessage, character string) (int64, bool, error) {
	if m.r.decrees == nil {
		return 0, false, nil
	}
	n, ok, err := m.r.decrees.FindException(ctx, workspace, classes.RequestMerge, target)
	if err != nil {
		return 0, false, err
	}
	if !ok { // an exception for another state of this PR (the head moved) expires unused
		if old, err := m.r.decrees.ExpireMoved(ctx, workspace, classes.RequestMerge, target); err != nil || old != 0 {
			if err != nil {
				return 0, false, err
			}
			return old, false, ghcli.ErrExceptionExpired
		}
		return 0, false, nil
	}
	err = m.r.decrees.Redeem(ctx, n, chain.Request{Workspace: workspace, Type: classes.RequestMerge, Target: target}, character, "merge")
	switch {
	case err == nil:
		return n, true, nil
	case errors.Is(err, decrees.ErrTargetChanged):
		return n, false, ghcli.ErrExceptionExpired
	case errors.Is(err, decrees.ErrEnded), errors.Is(err, decrees.ErrWrongRequest):
		return 0, false, nil // someone spent it first, or it is not for this request
	}
	return 0, false, err
}

// Ask opens the request of the merge in the chain.
func (m mergeHost) Ask(ctx context.Context, o chain.OpenRequest) (int64, error) {
	c := m.r.chain.Load()
	if c == nil {
		return 0, errNoApproval
	}
	return c.Request(ctx, o)
}

// Table is the table whose branch is the head of the PR.
func (m mergeHost) Table(ctx context.Context, workspace, headRef string) string {
	orc := m.r.orc.Load()
	if orc == nil || headRef == "" {
		return ""
	}
	all, err := orc.Tables.List(ctx, workspace)
	if err != nil {
		return ""
	}
	for _, t := range all {
		if t.Branch == headRef && !tables.Over(t.State) {
			return t.ID
		}
	}
	return ""
}
