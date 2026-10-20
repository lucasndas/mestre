package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/attachments"
	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/lockdown"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/digest"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/progress"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
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
	// GitHubWeb is github.com (empty: the real one), where the device flow and the
	// pages of the installations are.
	GitHubWeb string
	HTTP      *http.Client
	// WhisperURL is the whisper-server of this machine (http://127.0.0.1:<port>);
	// empty: no machine transcribes, and every voice message is delivered with
	// the reason. The installation sets it (docs/desenvolvimento/ligacao-de-producao.md).
	WhisperURL string
	// FFmpeg is the program that converts the audio (default "ffmpeg").
	FFmpeg string
	// AttachmentLimit is the biggest attachment (default 20 MB).
	AttachmentLimit int64
}

// Open implements Integration. It reads nothing from the vault and writes nothing
// to the Guild: a Citadel that starts suspended and steps down has done neither
// (the token of the bot is read when the Citadel acts).
func (s StandardIntegration) Open(_ context.Context, env IntegrationEnv) (IntegrationRun, error) {
	if s.GitHubBase == "" {
		s.GitHubBase = DefaultGitHubBase
	}
	if s.GitHubWeb == "" {
		s.GitHubWeb = DefaultGitHubWeb
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
	if err := r.githubSetup(); err != nil {
		return nil, err
	}
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
	r.decrees, err = decrees.New(decrees.Options{DB: env.DB, Clock: env.Clock, PINs: svc, Alerts: r.alert, Requests: lateChain{r}, Targets: decreeTargets{live: lateTargets{r}, repos: r.workspaceRepo}, Locked: r.lock.Locked, Logger: env.Log})
	if err != nil {
		return nil, fmt.Errorf("app: the decrees: %w", err)
	}
	r.routing = &telegram.Routing{}
	r.replies = &telegram.Replies{Users: svc}
	r.block = &telegram.BlockChannel{Users: svc}
	r.choose = &telegram.ChooseChannel{Users: svc}
	r.stall = &telegram.StallChannel{Users: svc}
	r.dc = &telegram.DecreeChannel{Svc: r.decrees, Users: svc, PINs: r.pins}
	r.gc = &telegram.GuildChannel{Users: svc, PINs: r.pins, Machines: env.Machines, SetExcluded: env.SetExcluded, Pending: env.Pending}
	r.dg = &telegram.DigestChannel{Users: svc, Decrees: r.dc}
	flow, err := approvals.New(approvals.Options{DB: env.DB, Clock: env.Clock, Chain: lateChain{r}, Users: svc, PINs: svc, Messenger: r.ac,
		Locked: r.lock.Locked, Targets: lateTargets{r}, Logger: env.Log})
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
	// routing decides the workspace of each message of a user.
	routing *telegram.Routing
	// replies takes the answers of the Grand Master back to the chat of the user.
	replies *telegram.Replies
	// block sends the messages that ask something of the users because a table is stopped.
	block *telegram.BlockChannel
	// choose asks a user a question with buttons (`mestre choose`).
	choose *telegram.ChooseChannel
	// stall warns the people of a table that works without delivering, with the button to pause it.
	stall *telegram.StallChannel
	dc    *telegram.DecreeChannel
	gc    *telegram.GuildChannel
	// fetch is the client of the Bot API the attachments are downloaded with, once the bot is made.
	fetch  atomic.Pointer[telegram.Client]
	dg     *telegram.DigestChannel
	ac     *telegram.ApprovalChannel
	alert  *telegram.OwnerAlerts
	notice *guild.DownNotice
	lock   *lockdown.Service
	pins   *telegram.PINRouter
	lc     *telegram.LockChannel
	quota  *github.Quota
	tokens *github.Tokens
	// personal and installs are the personal credentials and GitHub's side of the
	// stage apps (githubcompose.go).
	personal *github.Personal
	installs *github.Installs
	// approver and settings apply the approvals on GitHub (approvalhost.go), made
	// when the orchestration exists.
	approver atomic.Pointer[approvals.Approver]
	settings atomic.Pointer[approvals.Settings]

	fence atomic.Pointer[guild.Fence]
	chain atomic.Pointer[chain.Service]
	orc   atomic.Pointer[Orchestration]
	// progress is the message of progress of the workspaces, once the period has it.
	progress atomic.Pointer[progress.Service]
	closed   atomic.Bool

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

// lateTargets reads the live target of a request for the flow, from the
// orchestration of the period: the destructive requests, the PRs (the head and the
// tip of the base) and the settings of a repository (what the Owner approves is
// exactly what is applied). Any other type bound to a target is not approved (it
// cannot be verified).
type lateTargets struct{ r *stdRun }

func (t lateTargets) Current(ctx context.Context, req chain.Request) (json.RawMessage, error) {
	orc := t.r.orc.Load()
	if orc == nil {
		return nil, errors.New("app: the orchestration is not built")
	}
	switch req.Type {
	case classes.RequestDestructive:
		return orc.DestructiveTarget(ctx, req)
	case classes.RequestPR, classes.RequestMerge:
		return t.r.source().Current(ctx, req)
	case classes.RequestRepoSettings:
		return req.Target, nil
	}
	return nil, fmt.Errorf("app: no live target for requests of type %q", req.Type)
}

// workspaceRepo is the repository of a workspace, from the orchestration of the
// period.
func (r *stdRun) workspaceRepo(ctx context.Context, workspace string) (string, error) {
	orc := r.orc.Load()
	if orc == nil {
		return "", errors.New("app: the orchestration is not built")
	}
	return workspaceRepoOf{orc.Workspaces}.Repo(ctx, workspace)
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
	client := &telegram.Client{Token: token, BaseURL: r.s.TelegramBase, HTTP: r.s.HTTP}
	r.fetch.Store(client)
	bot, err := telegram.New(telegram.Options{
		DB: r.env.DB, Clock: r.env.Clock, Users: r.users, Alerts: r.alert, Strangers: r.alert, Gate: lateGate{r}, Logger: r.env.Log,
		Client: client,
		Router: r.routing, Commands: append(append(append(r.lc.Commands(), r.dc.Commands()...), r.gc.Commands()...), r.routing.Commands()...), Lock: r.lock, Mask: r.env.Masker.Mask, Machine: r.env.Machine,
		Actions:  mergeActions(r.ac.Actions(), r.lc.Actions(), r.dc.Actions(), r.gc.Actions(), r.dg.Actions(), r.routing.Actions(), r.block.Actions(), r.choose.Actions(), r.stall.Actions()),
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
	r.gc.Bind(bot)
	r.dg.Bind(bot)
	r.routing.Bind(bot)
	r.replies.Bind(bot)
	r.block.Bind(bot)
	r.choose.Bind(bot)
	r.stall.Bind(bot)
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
	o.MissionCompleted = func(ctx context.Context, workspace, _, _ string) {
		if p := r.progress.Load(); p != nil && !r.closed.Load() {
			go p.MissionEnded(context.WithoutCancel(ctx), workspace) // the quest does not wait for the bot
		}
	}
	o.ChainUsers = r.flow
	if o.Channels == nil {
		o.Channels = r.replies // the answer of `mestre reply` goes back to the chat of the user
	}
	if o.ChainStallPeople == nil {
		o.ChainStallPeople = r.stall
	}
	// the gate of freshness reads the rules of the base branch as the Grand Master
	if o.FreshnessRules == nil {
		o.FreshnessRules = stdRules{r}
	}
	// the facts of the PRs for the policy, and what applies on GitHub what people decide
	if o.ChainPRs == nil {
		o.ChainPRs = r.source()
	}
	sinks := map[classes.RequestType]chain.Sink{
		classes.RequestPR:           lateSink{func() chain.Sink { return soft(r.approver.Load()) }, r.alert.OwnerAlert},
		classes.RequestMerge:        lateSink{func() chain.Sink { return soft(r.approver.Load()) }, r.alert.OwnerAlert},
		classes.RequestRepoSettings: lateSink{func() chain.Sink { return soft(r.settings.Load()) }, r.alert.OwnerAlert},
	}
	for t, s := range o.ChainSinks { // a test that gives its own keeps it
		sinks[t] = s
	}
	o.ChainSinks = sinks
	// the GitHub of the workspaces, with the App Grão-Mestre; a test that gives its own keeps it
	gh := grandMasterGitHub{r}
	if _, off := o.GitHub.(unavailableGitHub); off {
		o.GitHub = gh
	}
	if o.Credentials == nil {
		o.Credentials = gh
	}
	if o.ChainGrants == nil {
		o.ChainGrants = r.decrees
	}
	if o.ChainSubDelegations == nil {
		o.ChainSubDelegations = r.decrees
	}
	if o.ChainPolicyDecrees == nil {
		o.ChainPolicyDecrees = r.decrees
	}
	if o.ChainDelegations == nil {
		o.ChainDelegations = r.decrees
	}
	o.Locked = r.lock.Locked
	if o.Messages == nil {
		o.Messages = inboxMessages{&inbox.Store{DB: r.env.DB}}
	}
	// the people and the channel of the inbox: the notices "Sistema" that find no
	// Grand Master and the answers of `mestre reply` go out by the bot
	if o.Users == nil {
		o.Users = inboxUsers{r}
	}
	if o.ChainSinks == nil {
		o.ChainSinks = map[classes.RequestType]chain.Sink{}
	}
	o.ChainSinks[classes.RequestGuildEntry] = r.entries()

	// the operational alerts of the workspaces reach the Administrators by the
	// bot; the log of the engine keeps the record as it did before the bot
	o.Alerts = adminAlerts{r: r, log: r.env.Log}
	o.Epics = stdEpics{r: r, reg: characters.Registry{DB: r.env.DB}}
}

// entries is the question to the Owner about the entry of a machine, over the
// chain the orchestration makes later and the Citadel the engine starts later.
func (r *stdRun) entries() EntryApprover {
	errNoCitadelYet := errors.New("app: no Citadel to take the answer about an entry")
	decide := r.env.DecideJoin
	if decide == nil {
		decide = func(context.Context, string, guild.Decision) error { return errNoCitadelYet }
	}
	return EntryApprover{Chain: r.chain.Load, Decide: decide, Log: r.env.Log,
		Tell: func(ctx context.Context, text string) { r.alert.OwnerAlert(ctx, text) }}
}

// GuildApprover implements GuildApprover: the Citadel asks the Owner about an
// entry through the flow of approval.
func (r *stdRun) GuildApprover() guild.Approver { return r.entries() }

// GuildLock implements GuildLocker: the lock of the epoch over the repository of
// the Guild in GitHub, once the repository was recorded; nil before.
func (r *stdRun) GuildLock(ctx context.Context) (guild.Lock, error) {
	l, err := github.NewLairLock(ctx, r.tokens, r.env.GuildID, r.s.GitHubBase, r.s.HTTP)
	if l == nil {
		return nil, err
	}
	return l, nil
}

// OwnerAlert, CharacterDecrees and Users make the run the people of the chain
// (chainPeople).
func (r *stdRun) OwnerAlert(ctx context.Context, text string) { r.alert.OwnerAlert(ctx, text) }

// DecreesChanged has the effective rules published again after the decrees in
// force changed.
func (r *stdRun) DecreesChanged(f func(context.Context)) { r.decrees.SetChanged(f) }

func (r *stdRun) CharacterDecrees() CharacterDecrees { return r.decrees }
func (r *stdRun) Users() *users.Service              { return r.users }

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
	r.orc.Store(orc)
	r.approvalSetup(orc)
	r.routing.To = inboxDeliverer{orc}
	if orc.Supervisor != nil {
		r.stall.Pauser = orc.Supervisor
	}
	if orc.TableRules != nil {
		r.dc.Rules = telegram.TableRulesOf(orc.TableRules)
	}
	r.decrees.SetReminder(r.dc)
	r.decrees.SetRevokeHooks(telegram.RevokeAllHooks{Rules: orc.TableRules, Users: r.users, Channel: r.dc,
		GrandMaster: func(ctx context.Context, workspace, text string) {
			gm, ok, err := orc.Registry.GrandMaster(ctx, workspace)
			if err != nil || !ok {
				return
			}
			if _, err := orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text}); err != nil {
				r.env.Log.Warn("app: the Grand Master was not told of the revocation", "workspace", workspace, "err", err)
			}
		},
		RerouteFn: func(ctx context.Context, workspace string) error {
			return orc.Chain.RerouteWorkspace(ctx, workspace, "o Dono revogou todas as delegações")
		}})
	// the validity and the reminder of the delegations are those of the policy file of each workspace
	if orc.Policy != nil {
		pol := orc.Policy
		r.decrees.SetDelegationTimes(func(ctx context.Context, workspace string) (time.Duration, time.Duration) {
			st, _, err := pol.Chain(ctx, workspace)
			if err != nil {
				return 0, 0
			}
			return st.DelegationValidity, st.DelegationReminder
		})
	}
	r.decrees.SetTableOpen(func(ctx context.Context, workspace, table string) (bool, error) {
		tb, err := orc.Tables.Get(ctx, workspace, table)
		if errors.Is(err, tables.ErrNoTable) {
			return false, nil
		}
		return err == nil && tb.State == tables.StateOpen, err
	})
	r.routing.Files = r.attachments(orc)
	var dgst *digest.Service
	if orc.Policy != nil && orc.Chain != nil {
		var err error
		dgst, err = digest.New(digest.Options{DB: r.env.DB, Clock: r.env.Clock, Source: &chain.Store{DB: r.env.DB}, Policy: orc.Policy, Inbox: digestInbox{orc},
			Directory: digest.UsersDirectory{Users: r.users}, Sender: r.dg, Rand: rand.New(rand.NewPCG(uint64(r.env.Clock.Now().UnixNano()), 0x6d6573747265)),
			Decrees: r.decrees, Rules: digestRules{orc}, Names: func(ctx context.Context, id string) string {
				if c, err := orc.Registry.Get(ctx, id); err == nil {
					return c.Name
				}
				return ""
			}, Mask: r.env.Masker.Mask, Zone: guildZone{r.env.DB}.Zone, Logger: r.env.Log})
		if err != nil {
			r.env.Log.Warn("app: the periodic summary is off", "err", err)
		} else {
			r.dg.Svc = dgst
		}
	}
	prog, err := progress.New(progress.Options{Clock: r.env.Clock, Tables: tables.Store{DB: r.env.DB}, Requests: &chain.Store{DB: r.env.DB},
		Workspaces: activeWorkspaces{orc}, Recipients: progressPeople{digest.UsersDirectory{Users: r.users}}, Sender: progressSender{r.dg},
		Mask: r.env.Masker.Mask, Logger: r.env.Log})
	if err != nil {
		r.env.Log.Warn("app: the message of progress is off", "err", err)
	} else {
		r.progress.Store(prog)
	}
	h := &ghcli.Handlers{
		Characters: orc.Registry, Clients: stdClients{r: r}, Repos: workspaceRepos{orc}, Tables: tableRefs{orc}, PRs: tablePRs{orc},
		Objects: &github.Objects{DB: r.env.DB, Clock: r.env.Clock}, Clock: r.env.Clock, Lease: stdLease{r}, Quota: stdQuota{r}, Locked: r.lock.Locked,
		Refused: func(ctx context.Context, c characters.Character, code, reason string) {
			gm, ok, err := orc.Registry.GrandMaster(ctx, c.Workspace)
			if err != nil || !ok {
				return
			}
			text := fmt.Sprintf("O envio da branch da mesa %s foi recusado (%s): %s. O motor não tenta outro caminho.", c.Table, code, reason)
			if _, err := orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: c.Workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text}); err != nil {
				r.env.Log.Warn("app: the Grand Master was not told of the refusal of GitHub", "workspace", c.Workspace, "err", err)
			}
		},
		Executed: r.decrees.Fulfilled, Heads: &github.ReviewedHeads{DB: r.env.DB, Clock: r.env.Clock}, Settings: stdSettings{r},
		Approver: lateApprover{r}, Merges: mergeHost{r}, Rulesets: lateRulesets{r},
	}
	if orc.BaseSync != nil {
		h.Send.Base = orc.BaseSync
	}
	if orc.EpicDeps != nil {
		h.Deps = orc.EpicDeps
	}
	// the acceptance of the Master of the table: no pull request while a
	// completed mission of the epic has a criterion with no proof
	h.Send.Criteria = quests.Acceptance{Store: &quests.Store{DB: r.env.DB}, Body: r.issueBody(orc)}
	h.Register(fwd)
	(&ghcli.Apps{Characters: orc.Registry, Store: &github.Registry{DB: r.env.DB}, Creator: appCreator{}}).Register(fwd)
	(&decrees.ProposeHandler{Service: r.decrees, Characters: orc.Registry, Message: inboxMessages{&inbox.Store{DB: r.env.DB}}.Source, Offer: r.dc}).Register(fwd)
	(&decrees.SubHandler{Service: r.decrees, Characters: orc.Registry}).Register(fwd)
	poll, perr := r.poller(orc)
	if perr != nil {
		r.env.Log.Warn("app: the reading of the events of GitHub is off", "err", perr)
	}
	msgs := inboxMessages{&inbox.Store{DB: r.env.DB}}
	r.choose.Answered = func(ctx context.Context, q telegram.ChoiceQuestion, by users.User, option string) error {
		gm, ok, err := orc.Registry.GrandMaster(ctx, q.Workspace)
		if err != nil || !ok {
			return errors.New("the workspace has no Grand Master")
		}
		it := telegram.AnswerItem(q, by, option)
		it.Target = gm.ID
		_, err = orc.Inbox.Enqueue(ctx, it)
		return err
	}
	(&blocking.ChooseHandler{Characters: orc.Registry, Chooser: chooser{r.choose}, User: msgs.User, Current: msgs.Current, Table: orc.Tables.Find}).Register(fwd)
	(&blocking.Handler{Characters: orc.Registry, Channel: r.block, Table: orc.Tables.Find, BlockedBy: orc.EpicDeps.Waiting, Owner: r.ownerName}).Register(fwd)
	return []ExternalFunc{func(ctx context.Context, _ *guild.Fence) error {
		// the decrees whose validity ran out end while the Citadel acts
		go func() { _ = r.decrees.Loop(ctx, time.Minute) }()
		if poll != nil {
			go poll.Run(ctx) // the events of GitHub, only while the lease holds
		}
		if dgst != nil {
			go func() { _ = dgst.Loop(ctx, time.Minute) }()
		}
		if p := r.progress.Load(); p != nil {
			go func() { _ = p.Loop(ctx, time.Minute) }()
		}
		bot, err := r.startBot(ctx)
		if err != nil {
			r.env.Log.Warn("app: the bot of the Telegram is off", "err", err)
			<-ctx.Done()
			return nil
		}
		go func() { // the answers kept while the bot was not up
			if _, err := orc.Inbox.ChannelUp(ctx, inbox.ChannelTelegram); err != nil && ctx.Err() == nil {
				r.env.Log.Warn("app: the answers kept for the Telegram were not sent", "err", err)
			}
		}()
		// the owners of the site credentials are told of their validity by the bot
		go func() { _ = r.expiryWatcher().Run(ctx) }()
		return bot.Run(ctx)
	}}
}

// ownerName is the name of the Owner, who acts on a credential ("o Dono" when
// the users cannot be read).
func (r *stdRun) ownerName(ctx context.Context, _ string) string {
	all, err := r.users.List(ctx)
	if err != nil {
		return "o Dono"
	}
	for _, u := range all {
		if u.Level == users.Owner && u.State == users.Active {
			return u.Name
		}
	}
	return "o Dono"
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

// issueBody reads the body of an issue as the App "Mestre", for the acceptance
// of the table master: what the Master took out of the issue stops being asked.
func (r *stdRun) issueBody(orc *Orchestration) func(ctx context.Context, workspace string, number int) (string, error) {
	return func(ctx context.Context, workspace string, number int) (string, error) {
		repo, err := workspaceRepos{orc}.Repo(ctx, workspace)
		if err != nil {
			return "", err
		}
		api, err := stdClients{r}.For(ctx, characters.Character{Role: characters.RoleTableMaster, Workspace: workspace}, repo)
		if err != nil {
			return "", err
		}
		d, err := api.IssueData(ctx, repo, number)
		return d.Body, err
	}
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

// inboxDeliverer takes a routed message to the inbox of the Grand Master of its
// workspace.
type inboxDeliverer struct{ orc *Orchestration }

func (d inboxDeliverer) Deliver(ctx context.Context, m telegram.Delivery) error {
	gm, ok, err := d.orc.Registry.GrandMaster(ctx, m.Workspace)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app: the workspace %s has no Grand Master now", m.Workspace)
	}
	_, err = d.orc.Inbox.Enqueue(ctx, telegram.NewItem(m, gm.ID))
	return err
}

// attachments is the reception of the files of the users: the Bot API through the
// client of the bot, the main clone of each workspace, and the transcription of
// the voice messages by the machines the installation names.
func (r *stdRun) attachments(orc *Orchestration) telegram.Files {
	var machines func(context.Context) []attachments.Machine
	if r.s.WhisperURL != "" {
		w := attachments.Whisper{BaseURL: r.s.WhisperURL, HTTP: r.s.HTTP, Prompt: "Conversa em português sobre desenvolvimento de software."}
		machines = func(context.Context) []attachments.Machine {
			return []attachments.Machine{{Name: "cidadela", Engine: w}}
		}
	}
	tr := attachments.NewTranscription(r.env.Clock, attachments.FFmpeg{Path: r.s.FFmpeg}, machines, "")
	svc, err := attachments.New(attachments.Options{DB: r.env.DB, Clock: r.env.Clock, Fetcher: lateFetcher{r}, Limit: r.s.AttachmentLimit, Transcriber: tr,
		Root: func(_ context.Context, workspace string) (string, error) {
			return orc.Workspaces.Layout().Principal(workspace), nil
		},
		Mask: r.env.Masker.Mask, Logger: r.env.Log})
	if err != nil {
		r.env.Log.Warn("app: the attachments are off", "err", err)
		return nil
	}
	return svc
}

// lateFetcher is the client of the Bot API, which exists once the bot is made.
type lateFetcher struct{ r *stdRun }

var errNoBot = errors.New("app: the bot of the Telegram is not up")

func (f lateFetcher) GetFile(ctx context.Context, id string) (string, int64, error) {
	c := f.r.fetch.Load()
	if c == nil {
		return "", 0, errNoBot
	}
	return c.GetFile(ctx, id)
}

func (f lateFetcher) Download(ctx context.Context, path string, limit int64) ([]byte, error) {
	c := f.r.fetch.Load()
	if c == nil {
		return nil, errNoBot
	}
	return c.Download(ctx, path, limit)
}

// digestInbox puts the list of the summary in the inbox of the Grão-Mestre.
type digestInbox struct{ orc *Orchestration }

func (d digestInbox) ToGrandMaster(ctx context.Context, workspace, text string) error {
	gm, ok, err := d.orc.Registry.GrandMaster(ctx, workspace)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app: the workspace %s has no Grand Master now", workspace)
	}
	_, err = d.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text})
	return err
}

// digestRules revokes the temporary table rules that a decision made.
type digestRules struct{ orc *Orchestration }

func (d digestRules) RevokeOfRequest(ctx context.Context, workspace, table string, request int64, by string) (int, error) {
	if d.orc.TableRules == nil {
		return 0, nil
	}
	rules, err := d.orc.TableRules.List(ctx, workspace, table)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rules {
		if r.Request != request {
			continue
		}
		if err := d.orc.TableRules.Revoke(ctx, r.ID, tablerules.Revoker{Administrator: true, ID: by}); err == nil {
			n++
		}
	}
	return n, nil
}

var (
	_ GuildApprover = (*stdRun)(nil)
	_ GuildLocker   = (*stdRun)(nil)
)

// adminAlerts is workspaces.Alerts over the bot: the notice goes to the Owner and
// to the Administrators who act in the workspace. While the bot is not up the alert
// waits in the queue of the alerts of the bot and goes out when it starts; with
// nobody to tell it stays in the log (workspace and kind only: the detail can carry
// the output of git).
type adminAlerts struct {
	r   *stdRun
	log *slog.Logger
}

func (a adminAlerts) Operational(ctx context.Context, al workspaces.Alert) error {
	a.log.Warn("app: operational alert", "workspace", al.Workspace, "kind", string(al.Kind))
	told, err := a.r.alert.AdminAlert(ctx, al.Workspace, al.Detail)
	switch {
	case errors.Is(err, telegram.ErrQueued):
		return nil // it waits for the bot, once: the caller does not send it again
	case err == nil && told == 0:
		a.log.Warn("app: no Administrator could be told of an operational alert", "workspace", al.Workspace, "kind", string(al.Kind))
	}
	return err
}
