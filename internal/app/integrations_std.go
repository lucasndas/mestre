package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/lockdown"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/users"
)

// TelegramTokenSecret is the name of the token of the bot in the vault.
const TelegramTokenSecret = "telegram_bot_token"

// DefaultGitHubBase is the API of GitHub.
const DefaultGitHubBase = "https://api.github.com"

// StandardIntegration is the integration of production: the users, the flow of
// approval, the bot of the Telegram (made when the Citadel acts, if the vault
// has its token) and the commands of issues and PRs. Telegram and GitHub are the
// real ones unless the tests give another client and base URL.
type StandardIntegration struct {
	// TelegramBase and GitHubBase are the base URLs (empty: the real ones);
	// HTTP is the client of both (nil: one with a time limit).
	TelegramBase, GitHubBase string
	HTTP                     *http.Client
}

// Open implements Integration. It reads nothing from the vault and writes nothing
// to the Guild: a Citadel that starts suspended and steps down has done neither
// (the token of the bot is read when the Citadel acts).
func (s StandardIntegration) Open(_ context.Context, env IntegrationEnv) (IntegrationRun, error) {
	if s.GitHubBase == "" {
		s.GitHubBase = DefaultGitHubBase
	}
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if env.Log == nil {
		env.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	r := &stdRun{s: s, env: env, quota: &github.Quota{Clock: env.Clock}}
	r.tokens = &github.Tokens{Apps: &github.Registry{DB: env.DB}, Keys: env.Vault, Clock: env.Clock, BaseURL: s.GitHubBase, HTTP: s.HTTP}
	r.alert = &telegram.OwnerAlerts{Clock: env.Clock}
	svc, err := users.New(users.Options{DB: env.DB, Clock: env.Clock, Alerts: r.alert, Approvals: lateApprovals{r}})
	if err != nil {
		return nil, fmt.Errorf("app: the users: %w", err)
	}
	r.users = svc
	r.alert.Users = svc
	r.lock, err = lockdown.New(lockdown.Options{DB: env.DB, Clock: env.Clock, Users: svc, Tell: r.alert.Tell, Logger: env.Log})
	if err != nil {
		return nil, fmt.Errorf("app: the lock of the channel: %w", err)
	}
	r.pins = &telegram.PINRouter{Users: svc}
	r.ac = &telegram.ApprovalChannel{Users: svc, PINs: r.pins}
	r.lc = &telegram.LockChannel{Svc: r.lock, Users: svc, PINs: r.pins}
	// what was kept while the Mestre was locked goes when it is not
	r.lock.OnChange(func(locked bool) {
		if !locked {
			if bot := r.currentBot(); bot != nil {
				if err := bot.ReleaseHeld(context.Background()); err != nil {
					env.Log.Warn("app: the messages kept while the Mestre was locked were not delivered", "err", err)
				}
			}
		}
	})
	if env.SetLocked != nil {
		env.SetLocked(r.lock.Locked)
	}
	r.decrees, err = decrees.New(decrees.Options{DB: env.DB, Clock: env.Clock, PINs: svc, Alerts: r.alert, Requests: lateChain{r}, Locked: r.lock.Locked, Logger: env.Log})
	if err != nil {
		return nil, fmt.Errorf("app: the decrees: %w", err)
	}
	r.dc = &telegram.DecreeChannel{Svc: r.decrees, Users: svc, PINs: r.pins}
	flow, err := approvals.New(approvals.Options{DB: env.DB, Clock: env.Clock, Chain: lateChain{r}, Users: svc, PINs: svc, Messenger: r.ac,
		Locked: r.lock.Locked, Logger: env.Log})
	if err != nil {
		return nil, fmt.Errorf("app: the flow of approval: %w", err)
	}
	r.flow = flow
	r.notice = &guild.DownNotice{Tell: func(ctx context.Context, text string) { r.alert.OwnerAlert(ctx, text) }, Clock: env.Clock,
		Queued: func() int {
			if f := r.fence.Load(); f != nil {
				return f.Queued()
			}
			return 0
		},
		LastError: func() error {
			if f := r.fence.Load(); f != nil {
				return f.LastError()
			}
			return nil
		}}
	return r, nil
}

type stdRun struct {
	s   StandardIntegration
	env IntegrationEnv

	users *users.Service
	flow  *approvals.Flow
	// decrees are the decrees of the users and dc their channel over the bot.
	decrees *decrees.Service
	dc      *telegram.DecreeChannel
	ac      *telegram.ApprovalChannel
	alert   *telegram.OwnerAlerts
	notice  *guild.DownNotice
	lock    *lockdown.Service
	pins    *telegram.PINRouter
	lc      *telegram.LockChannel
	quota   *github.Quota
	tokens  *github.Tokens

	fence  atomic.Pointer[guild.Fence]
	chain  atomic.Pointer[chain.Service]
	closed atomic.Bool

	botMu sync.Mutex
	bot   *telegram.Bot
}

var _ IntegrationRun = (*stdRun)(nil)

// lateGate is the gate of the bot: the fence is made after the integration, and
// until it exists nothing goes out.
type lateGate struct{ r *stdRun }

func (g lateGate) Allowed(ctx context.Context) bool {
	f := g.r.fence.Load()
	return !g.r.closed.Load() && f != nil && f.Gate(guild.ExternalTelegram).Allowed(ctx)
}

// lateChain ties the flow to the chain, which the orchestration makes later.
type lateChain struct{ r *stdRun }

func (c lateChain) svc() (*chain.Service, error) {
	if s := c.r.chain.Load(); s != nil {
		return s, nil
	}
	return nil, errors.New("app: the chain of approval is not built")
}

func (c lateChain) Get(ctx context.Context, id int64) (chain.Request, error) {
	s, err := c.svc()
	if err != nil {
		return chain.Request{}, err
	}
	return s.Get(ctx, id)
}

func (c lateChain) DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	s, err := c.svc()
	if err != nil {
		return chain.Outcome{}, err
	}
	return s.DecideByPerson(ctx, d)
}

func (c lateChain) Invalidate(ctx context.Context, id int64, why string) error {
	s, err := c.svc()
	if err != nil {
		return err
	}
	return s.Invalidate(ctx, id, why)
}

type lateApprovals struct{ r *stdRun }

// InvalidateBy is what happens to what a user left when he is suspended,
// removed or loses the level: the approvals not executed and the decrees.
func (a lateApprovals) InvalidateBy(ctx context.Context, id string) error {
	err := a.r.flow.InvalidateBy(ctx, id)
	if derr := a.r.decrees.InvalidateBy(ctx, id); derr != nil {
		err = errors.Join(err, derr)
	}
	return err
}

// startBot makes the bot, reading its token from the vault: it runs only when the
// fence lets the Citadel act. Without the token there is no bot, and the
// questions to the people wait for their time (they are never approved).
func (r *stdRun) startBot(ctx context.Context) (*telegram.Bot, error) {
	r.botMu.Lock()
	defer r.botMu.Unlock()
	if r.bot != nil {
		return r.bot, nil
	}
	var token string
	err := r.env.Vault.Use(ctx, TelegramTokenSecret, "the bot of the Telegram", func(v []byte) error {
		token = string(v)
		return nil
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("the token of the Telegram bot (%s) could not be read: %w", TelegramTokenSecret, err)
	case token == "":
		return nil, fmt.Errorf("no token of the Telegram bot in the vault (%s)", TelegramTokenSecret)
	}
	bot, err := telegram.New(telegram.Options{
		DB: r.env.DB, Clock: r.env.Clock, Users: r.users, Alerts: r.alert, Strangers: r.alert, Gate: lateGate{r}, Logger: r.env.Log,
		Client:   &telegram.Client{Token: token, BaseURL: r.s.TelegramBase, HTTP: r.s.HTTP},
		Commands: append(r.lc.Commands(), r.dc.Commands()...), Lock: r.lock, Mask: r.env.Masker.Mask,
		Actions:  mergeActions(r.ac.Actions(), r.lc.Actions(), r.dc.Actions()),
		Captures: mergeCaptures(r.ac.Captures(), map[string]telegram.CaptureFunc{telegram.PINKind: r.pins.Capture()}),
		OnConflict: func(context.Context) {
			if f := r.fence.Load(); f != nil {
				f.TelegramConflict()
			}
		},
	})
	if err != nil {
		return nil, err
	}
	r.ac.Bind(bot, r.flow)
	r.lc.Bind(bot)
	r.dc.Bind(bot)
	r.alert.Bind(bot)
	r.bot = bot
	return bot, nil
}

func (r *stdRun) currentBot() *telegram.Bot {
	r.botMu.Lock()
	defer r.botMu.Unlock()
	return r.bot
}

func mergeActions(ms ...map[string]telegram.Action) map[string]telegram.Action {
	out := map[string]telegram.Action{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func mergeCaptures(ms ...map[string]telegram.CaptureFunc) map[string]telegram.CaptureFunc {
	out := map[string]telegram.CaptureFunc{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// Extend implements IntegrationRun: the people of the chain of approval.
func (r *stdRun) Extend(o *OrchestrationOptions) {
	o.ChainUsers = r.flow
	if o.ChainGrants == nil {
		o.ChainGrants = r.decrees
	}
	if o.ChainDelegations == nil {
		o.ChainDelegations = r.decrees
	}
	o.Locked = r.lock.Locked
}

// BindFence implements IntegrationRun.
func (r *stdRun) BindFence(f *guild.Fence) { r.fence.Store(f) }

// FenceChanged implements IntegrationRun.
func (r *stdRun) FenceChanged(s guild.FenceState) { r.notice.Changed(s) }

// Close implements IntegrationRun: what the commands in the local channel keep
// of this period stops working (the token, the fence), and the bot is let go.
func (r *stdRun) Close() {
	r.closed.Store(true)
	r.alert.Bind(nil)
	r.botMu.Lock()
	r.bot = nil
	r.botMu.Unlock()
}

// Attach implements IntegrationRun.
func (r *stdRun) Attach(orc *Orchestration, fwd *localchan.LocalForwarder) []ExternalFunc {
	r.chain.Store(orc.Chain)
	if orc.TableRules != nil {
		r.dc.Rules = telegram.TableRulesOf(orc.TableRules)
	}
	h := &ghcli.Handlers{
		Characters: orc.Registry, Clients: stdClients{r: r}, Repos: workspaceRepos{orc}, Tables: tableRefs{orc}, PRs: tablePRs{orc},
		Objects: &github.Objects{DB: r.env.DB, Clock: r.env.Clock}, Clock: r.env.Clock, Lease: stdLease{r}, Quota: stdQuota{r}, Locked: r.lock.Locked,
		Executed: r.decrees.Fulfilled,
	}
	h.Register(fwd)
	return []ExternalFunc{func(ctx context.Context, _ *guild.Fence) error {
		// the decrees whose validity ran out end while the Citadel acts
		go func() { _ = r.decrees.Loop(ctx, time.Minute) }()
		bot, err := r.startBot(ctx)
		if err != nil {
			r.env.Log.Warn("app: the bot of the Telegram is off", "err", err)
			<-ctx.Done()
			return nil
		}
		return bot.Run(ctx)
	}}
}

// stdLease is the lease of the epoch over GitHub: the fence of the period.
type stdLease struct{ r *stdRun }

var errNoFence = errors.New("app: this period has no fence to act under")

func (l stdLease) Allow() error {
	f := l.r.fence.Load()
	if f == nil || l.r.closed.Load() {
		return errNoFence
	}
	return f.Allow(guild.ExternalGitHub)
}

func (l stdLease) Do(ctx context.Context, name string, fn func(context.Context) error) (bool, error) {
	f := l.r.fence.Load()
	if f == nil || l.r.closed.Load() {
		return false, errNoFence
	}
	return f.Do(ctx, guild.ExternalGitHub, name, fn)
}

// stdClients gives the GitHub API with the App of who asks.
type stdClients struct{ r *stdRun }

// Permissions of the token of the commands of issues and PRs.
var issuePermissions = map[string]string{"issues": "write", "pull_requests": "write", "checks": "read"}

func (c stdClients) identity(ctx context.Context, ch characters.Character) (github.Identity, error) {
	return github.Identities{Apps: &github.Registry{DB: c.r.env.DB}}.For(ctx, github.Who{Role: string(ch.Role), Class: ch.Class})
}

func (c stdClients) For(ctx context.Context, ch characters.Character, _ string) (github.PullAPI, error) {
	id, err := c.identity(ctx, ch)
	if err != nil {
		return nil, err
	}
	tokens := c.r.tokens
	return &github.REST{BaseURL: c.r.s.GitHubBase, HTTP: c.r.s.HTTP, Quota: c.r.quota, App: id.App, Token: func(ctx context.Context, repo string) (string, error) {
		if c.r.closed.Load() {
			return "", errNoFence
		}
		tok, err := tokens.Installation(ctx, github.Request{App: id.App, Repo: repo, Permissions: issuePermissions})
		if err != nil {
			return "", err
		}
		return tok.Secret(), nil
	}}, nil
}

// stdQuota says whether the App of a character is under its quota.
type stdQuota struct{ r *stdRun }

func (q stdQuota) Low(ctx context.Context, ch characters.Character) (time.Time, bool) {
	id, err := stdClients(q).identity(ctx, ch)
	if err != nil {
		return time.Time{}, false
	}
	return q.r.quota.Low(id.App)
}

type workspaceRepos struct{ orc *Orchestration }

func (w workspaceRepos) Repo(ctx context.Context, workspace string) (string, error) {
	ws, err := w.orc.Workspaces.Get(ctx, workspace)
	if err != nil {
		return "", err
	}
	return ws.Repo.String(), nil
}

// tablePRs gives the PR of a table.
type tablePRs struct{ orc *Orchestration }

func (t tablePRs) PRNumber(ctx context.Context, key string) (int, bool, error) {
	ws, id, ok := cutKey(key)
	if !ok {
		return 0, false, nil
	}
	tb, err := t.orc.Tables.Get(ctx, ws, id)
	if errors.Is(err, tables.ErrNoTable) {
		return 0, false, nil
	}
	return tb.PRNumber, tb.PRNumber != 0, err
}

// tableRefs says where a table works: the repository of its workspace, its epic
// and its branch. The key is "<workspace>/<table>" (ghcli.TableKey).
type tableRefs struct{ orc *Orchestration }

func (t tableRefs) Epic(ctx context.Context, key string) (github.TableRef, bool, error) {
	ws, id, ok := cutKey(key)
	if !ok {
		return github.TableRef{}, false, nil
	}
	tb, err := t.orc.Tables.Get(ctx, ws, id)
	if errors.Is(err, tables.ErrNoTable) {
		return github.TableRef{}, false, nil
	}
	if err != nil {
		return github.TableRef{}, false, err
	}
	w, err := t.orc.Workspaces.Get(ctx, ws)
	if err != nil {
		return github.TableRef{}, false, err
	}
	return github.TableRef{Repo: w.Repo.String(), Epic: tb.Epic, Branch: tb.Branch}, true, nil
}

func cutKey(key string) (workspace, table string, ok bool) {
	for i := range key {
		if key[i] == '/' {
			return key[:i], key[i+1:], i > 0 && i+1 < len(key)
		}
	}
	return "", "", false
}
