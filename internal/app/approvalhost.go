package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/sessionzero"
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
type lateSink struct {
	get   func() chain.Sink
	alert func(ctx context.Context, text string)
}

func (l lateSink) Decided(ctx context.Context, res chain.Result) error {
	s := l.get()
	if s == nil {
		return errNoApproval
	}
	err := s.Decided(ctx, res)
	if err != nil && res.Approved && l.alert != nil {
		var ref *chain.Refusal
		why := "veja o log do motor"
		if errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired {
			why = ref.Message
		}
		l.alert(ctx, fmt.Sprintf("A aprovação do pedido #%d foi dada, mas não foi aplicada no GitHub: %s.", res.Request.ID, why))
	}
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

// lateTargets gives the flow the live target of a PR, which needs the mirrors of
// the orchestration made after it.
type lateTargets struct{ r *stdRun }

func (t lateTargets) Current(ctx context.Context, req chain.Request) (json.RawMessage, error) {
	if req.Type == classes.RequestRepoSettings {
		// what the Owner approves is exactly what Settings applies: the body is in the target
		// itself and there is no live state to compare it with
		return req.Target, nil
	}
	return t.r.source().Current(ctx, req)
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
			orc := r.orcp.Load()
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
	r.orcp.Store(orc)
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
