package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/basesync"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/users"
)

// DefaultGitHubWeb is github.com, where the device flow and the pages of the
// installations live.
const DefaultGitHubWeb = "https://github.com"

// The permissions of the token of the App "Grão-Mestre" for what only reads.
var (
	// rulesPermissions read the rules of a branch (GET /rules/branches/{branch}).
	rulesPermissions = map[string]string{"metadata": "read", "administration": "read"}
	// pollPermissions read issues, PRs, reviews and checks for the events.
	pollPermissions = map[string]string{"metadata": "read", "issues": "read", "pull_requests": "read", "checks": "read", "statuses": "read"}
)

// gmApp is the name of the App "Grão-Mestre" ("" while it does not exist yet).
func (r *stdRun) gmApp(ctx context.Context) string {
	a, err := (&github.Registry{DB: r.env.DB}).Fixed(ctx, github.RoleGrandMaster)
	if err != nil {
		return ""
	}
	return a.Name
}

// gmRest is the REST API with the token of the App "Grão-Mestre" (whichever name
// it got) for a repository and the permissions the reading uses; app is its name
// for the quota ("" when it is not known yet: the quota is then followed apart).
func (r *stdRun) gmRest(app string, perms map[string]string) *github.REST {
	return r.restOf(github.RoleGrandMaster, app, perms)
}

// roleRest is the same for a fixed App by its role, named for the quota as it is
// when the client is made.
func (r *stdRun) roleRest(role string, perms map[string]string) *github.REST {
	name := ""
	if a, err := (&github.Registry{DB: r.env.DB}).Fixed(context.Background(), role); err == nil {
		name = a.Name
	}
	return r.restOf(role, name, perms)
}

// restOf is the client of the fixed App of a role; its name is looked up at each
// token, since the App may be made after the client.
func (r *stdRun) restOf(role, app string, perms map[string]string) *github.REST {
	return &github.REST{BaseURL: r.s.GitHubBase, HTTP: r.s.HTTP, Quota: r.quota, App: app,
		Token: func(ctx context.Context, repo string) (string, error) {
			if r.closed.Load() {
				return "", errNoFence
			}
			a, err := (&github.Registry{DB: r.env.DB}).Fixed(ctx, role)
			if err != nil {
				return "", fmt.Errorf("the fixed App %s: %w", role, err)
			}
			tok, err := r.tokens.Installation(ctx, github.Request{App: a.Name, Repo: repo, Permissions: perms})
			if err != nil {
				return "", err
			}
			return tok.Secret(), nil
		}}
}

// stdRules reads what the base branch of a repository asks of a PR, for the gate
// of freshness (basesync.Rules).
type stdRules struct{ r *stdRun }

var _ basesync.Rules = stdRules{}

func (s stdRules) ReadRules(ctx context.Context, _, repo, branch string) (github.ReviewRules, error) {
	return s.r.gmRest(s.r.gmApp(ctx), rulesPermissions).ReadRules(ctx, repo, branch)
}

// ownerID is the id of the Owner.
func (r *stdRun) ownerID(ctx context.Context) (string, error) {
	all, err := r.users.List(ctx)
	if err != nil {
		return "", err
	}
	for _, u := range all {
		if u.Level == users.Owner && u.State == users.Active {
			return u.ID, nil
		}
	}
	return "", errors.New("app: the Owner is not registered")
}

// Personal is the personal credentials of the users (the device flow of the App
// "Grão-Mestre"): the panel and the bot call Begin and Poll, and the approvals
// and the installations use Client.
func (r *stdRun) Personal() *github.Personal { return r.personal }

// Installations is GitHub's side of the stage apps of the session zero, over the
// personal credential of the Owner.
func (r *stdRun) Installations() sessionzero.Installations {
	return sessionzero.GitHubInstallations{Source: r.installs}
}

// githubSetup makes the pieces of GitHub that need the users and the vault.
func (r *stdRun) githubSetup() error {
	apps := &github.Registry{DB: r.env.DB, Clock: r.env.Clock}
	var err error
	r.personal, err = github.NewPersonal(github.PersonalOptions{DB: r.env.DB, Clock: r.env.Clock, Apps: apps, Users: r.users, Vault: r.env.Vault,
		API: r.s.GitHubBase, Web: r.s.GitHubWeb, HTTP: r.s.HTTP, Logger: r.env.Log})
	if err != nil {
		return fmt.Errorf("app: the personal credentials: %w", err)
	}
	r.installs = &github.Installs{Apps: apps, Tokens: r.tokens, Personal: r.personal, Owner: r.ownerID, API: r.s.GitHubBase, Web: r.s.GitHubWeb, HTTP: r.s.HTTP}
	return nil
}

// ghSituation says how busy a workspace is, from its tables.
type ghSituation struct{ orc *Orchestration }

var _ github.Situation = ghSituation{}

func (s ghSituation) open(ctx context.Context, workspace string) []tables.Table {
	all, err := s.orc.Tables.List(ctx, workspace)
	if err != nil {
		s.orc.log.Warn("app: the tables were not listed for the reading of GitHub", "workspace", workspace, "err", err)
		return nil
	}
	var out []tables.Table
	for _, t := range all {
		if !tables.Over(t.State) {
			out = append(out, t)
		}
	}
	return out
}

func (s ghSituation) Mode(ctx context.Context, w github.Watch) github.Mode {
	if len(s.open(ctx, w.Workspace)) > 0 {
		return github.ModeAttentive
	}
	return github.ModeIdle
}

func (s ghSituation) TablePRs(ctx context.Context, w github.Watch) []github.TablePR {
	var out []github.TablePR
	for _, t := range s.open(ctx, w.Workspace) {
		if t.PRNumber != 0 && !t.Merged {
			out = append(out, github.TablePR{Number: t.PRNumber, Table: t.ID})
		}
	}
	return out
}

// TableOf is the table of an issue (the epic) or of a PR of the repository.
func (s ghSituation) TableOf(ctx context.Context, repo string, number int) string {
	all, err := s.orc.Workspaces.List(ctx)
	if err != nil {
		return ""
	}
	for _, w := range all {
		if !strings.EqualFold(w.Repo.String(), repo) {
			continue
		}
		for _, t := range s.open(ctx, w.Slug) {
			if t.Epic == number || t.PRNumber == number {
				return t.ID
			}
		}
	}
	return ""
}

// ghMerges confirms a merge in the mirror and marks the table (github.Merges).
type ghMerges struct{ orc *Orchestration }

var _ github.Merges = ghMerges{}

func (m ghMerges) Reachable(ctx context.Context, w github.Watch, sha string) (bool, error) {
	ws, err := m.orc.Workspaces.Get(ctx, w.Workspace)
	if err != nil {
		return false, err
	}
	base := ws.DefaultBranch
	if all, err := m.orc.Tables.List(ctx, w.Workspace); err == nil {
		for _, t := range all { // a table may go into another base than the default one
			if t.PRHead == sha && t.BaseBranch != "" {
				base = t.BaseBranch
			}
		}
	}
	mirror, err := m.orc.Workspaces.OpenMirror(ctx, w.Workspace)
	if err != nil {
		return false, err
	}
	return mirror.IsAncestor(ctx, sha, "refs/remotes/origin/"+base)
}

func (m ghMerges) Merged(ctx context.Context, w github.Watch, table string, _ int) error {
	t, err := m.orc.Tables.Get(ctx, w.Workspace, table)
	if err != nil {
		return err
	}
	_, err = m.orc.Tables.MergeConfirmed(ctx, w.Workspace, table, t.PRHead)
	return err
}

// watches are the active workspaces, read from their creation on.
func watches(orc *Orchestration) func(ctx context.Context) ([]github.Watch, error) {
	return func(ctx context.Context) ([]github.Watch, error) {
		all, err := orc.Workspaces.List(ctx)
		if err != nil {
			return nil, err
		}
		var out []github.Watch
		for _, w := range all {
			if w.State == workspaces.StateActive {
				out = append(out, github.Watch{Workspace: w.Slug, Repo: w.Repo.String(), Since: w.CreatedAt})
			}
		}
		return out, nil
	}
}

// poller makes the reader of the events of GitHub: what it finds goes to the
// Grand Master and, for a changed PR, wakes the step sync.
func (r *stdRun) poller(orc *Orchestration) (*github.Poller, error) {
	if orc.Workspaces == nil || orc.Tables == nil || orc.Inbox == nil {
		return nil, errors.New("the orchestration has no workspaces, tables or inbox to read the events for")
	}
	sinks := github.Sinks{inbox.GitHubSink{S: orc.Inbox}, isolated{r.reviews(), r.env.Log, "reviews"}}
	if orc.BaseSync != nil {
		sinks = append(sinks, basesync.EventSink{Base: orc.BaseSync})
	}
	if orc.EpicDeps != nil { // an epic closed on GitHub tells the tables that wait for it
		sinks = append(sinks, epicClosedSink{orc.EpicDeps})
	}
	app := r.gmApp(context.Background())
	gm := r.gmRest(app, pollPermissions)
	return github.NewPoller(github.PollerOptions{
		DB: r.env.DB, Clock: r.env.Clock, API: r.s.GitHubBase, HTTP: r.s.HTTP, Token: gm.Token, Objects: &github.Objects{DB: r.env.DB, Clock: r.env.Clock},
		Sink: sinks, Merges: ghMerges{orc}, Situation: ghSituation{orc}, Watches: watches(orc), Quota: r.quota, App: app,
		Alert: func(ctx context.Context, text string) { r.alert.OwnerAlert(ctx, text) }, Logger: r.env.Log,
	})
}

// GuildReconcile implements GuildReconciler: the new Citadel reads the comments
// the Apps of the Guild made on the repositories of the active workspaces in the
// last 30 minutes (with the App Grão-Mestre) and records the operations it does
// not know.
func (r *stdRun) GuildReconcile(orc *Orchestration) func(ctx context.Context) error {
	var rec *github.Reconciler
	var once sync.Once
	return func(ctx context.Context) error {
		if orc.Workspaces == nil {
			return errors.New("the orchestration has no workspaces to reconcile")
		}
		once.Do(func() {
			rec = &github.Reconciler{
				Objects: &github.Objects{DB: r.env.DB, Clock: r.env.Clock}, Clock: r.env.Clock,
				API: r.gmRest(r.gmApp(ctx), pollPermissions),
				Repos: func(ctx context.Context) ([]string, error) {
					var out []string
					ws, err := watches(orc)(ctx)
					for _, w := range ws {
						out = append(out, w.Repo)
					}
					return out, err
				},
				Authors: func(ctx context.Context) (map[string]bool, error) {
					all, err := (&github.Registry{DB: r.env.DB}).List(ctx)
					out := map[string]bool{}
					for _, a := range all {
						if a.Slug != "" {
							out[strings.ToLower(a.Slug)+"[bot]"] = true
						}
					}
					return out, err
				},
			}
		})
		return rec.Reconcile(ctx)
	}
}
