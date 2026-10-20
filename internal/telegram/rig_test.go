package telegram_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/glossary"
	"github.com/lucasndas/mestre/internal/lockdown"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/fakechannel"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

const token = "123456:fake-bot-token"

type router struct {
	mu  sync.Mutex
	got []telegram.Inbound
}

func (r *router) Deliver(_ context.Context, in telegram.Inbound) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, in)
	return nil
}

func (r *router) all() []telegram.Inbound {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telegram.Inbound(nil), r.got...)
}

func (r *router) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

type alerts struct {
	mu           sync.Mutex
	unauthorized []int64
	owner        []string
}

func (a *alerts) Unauthorized(_ context.Context, id int64, _ string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unauthorized = append(a.unauthorized, id)
}

func (a *alerts) OwnerAlert(_ context.Context, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owner = append(a.owner, text)
}

func (a *alerts) Tell(context.Context, string, string) {}

type world struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	users  *users.Service
	owner  users.User
	fake   *faketelegram.Server
	bot    *telegram.Bot
	router *router
	alerts *alerts
	opts   telegram.Options
	gc     *telegram.GuildChannel
	gf     *guildFake
	next   int64
	// The flow of approval over the bot (rigOpts.approvals).
	lock  *lockdown.Service
	lc    *telegram.LockChannel
	oa    *telegram.OwnerAlerts
	ac    *telegram.ApprovalChannel
	flow  *approvals.Flow
	chain *fakechannel.Chain
	// The decrees over the bot (rigOpts.decrees).
	ds        *decrees.Service
	dc        *telegram.DecreeChannel
	rules     *tablerules.Service
	routing   *telegram.Routing
	bc        *telegram.BlockChannel
	cc        *telegram.ChooseChannel
	answers   []inbox.NewItem
	sc        *telegram.StallChannel
	pauser    *fakeTablePauser
	dg        *telegram.DigestChannel
	delivered *deliveries
	// requests are the approval requests the decrees read.
	requests requests
}

type requests map[int64]chain.Request

func (r requests) Get(_ context.Context, id int64) (chain.Request, error) {
	if q, ok := r[id]; ok {
		return q, nil
	}
	return chain.Request{}, errors.New("no such request")
}

type rigOpts struct {
	terms    bool
	commands []telegram.Command
	actions  map[string]telegram.Action
	captures map[string]telegram.CaptureFunc
	mask     func(string) string
	// approvals puts the flow of approval over the bot.
	approvals bool
	// ownerAlerts puts the real alerts of the Owner (grouped) in the place of the fake.
	ownerAlerts bool
	// lockdown puts /travar and /destravar over the bot, with a real lock.
	lockdown bool
	// routing puts the routing of the messages by workspace over the bot, with a
	// recorder as the Grand Masters.
	routing bool
	// block puts the messages that block a table over the bot.
	block bool
	// decrees puts the decrees (/decretos, the confirmation, the PIN) over the bot.
	decrees bool
	// guild puts /guilda over the bot, over a fake Guild (guildFake).
	guild bool
	// digest puts the summary buttons over the bot (it needs decrees).
	digest bool
}

func newWorld(t *testing.T, ro ...rigOpts) *world {
	t.Helper()
	var ropt rigOpts
	if len(ro) > 0 {
		ropt = ro[0]
	}
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := &world{t: t, ctx: ctx, clk: clk, db: db, router: &router{}, alerts: &alerts{}, next: 1000}
	w.users, err = users.New(users.Options{DB: db, Clock: clk, Alerts: w.alerts, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if w.owner, err = w.users.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	w.fake, err = faketelegram.New(clk, t, token)
	if err != nil {
		t.Fatal(err)
	}
	if ropt.approvals {
		w.ac = &telegram.ApprovalChannel{Users: w.users}
		ropt.actions, ropt.captures = w.ac.Actions(), w.ac.Captures()
		for _, slug := range []string{"loja", "blog"} {
			if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
				VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
				t.Fatal(err)
			}
		}
	}
	w.opts = telegram.Options{DB: db, Clock: clk, Client: &telegram.Client{Token: token, HTTP: &http.Client{Transport: fakenet.HandlerTransport(w.fake)}},
		Users: w.users, Alerts: w.alerts, Router: w.router, Commands: ropt.commands, Actions: ropt.actions, Captures: ropt.captures, Mask: ropt.mask, PollTimeout: time.Millisecond}
	if ropt.terms {
		w.opts.Terms = glossary.Terms
	}
	if ropt.lockdown {
		router := &telegram.PINRouter{Users: w.users}
		var err error
		w.lock, err = lockdown.New(lockdown.Options{DB: db, Clock: clk, Users: w.users, Tell: func(ctx context.Context, id, text string) { w.tell(ctx, id, text) }})
		if err != nil {
			t.Fatal(err)
		}
		w.lc = &telegram.LockChannel{Svc: w.lock, Users: w.users, PINs: router}
		w.opts.Commands = append(append([]telegram.Command(nil), ropt.commands...), w.lc.Commands()...)
		w.opts.Actions = map[string]telegram.Action{}
		for k, v := range ropt.actions {
			w.opts.Actions[k] = v
		}
		for k, v := range w.lc.Actions() {
			w.opts.Actions[k] = v
		}
		w.opts.Captures = map[string]telegram.CaptureFunc{telegram.PINKind: router.Capture()}
		w.opts.Lock = w.lock
	}
	if ropt.ownerAlerts {
		w.oa = &telegram.OwnerAlerts{Users: w.users, Clock: clk}
		w.opts.Alerts, w.opts.Strangers = w.oa, w.oa
	}
	if ropt.routing {
		for _, slug := range []string{"loja", "blog", "shop"} {
			if _, err := db.Writer().ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
				VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
				t.Fatal(err)
			}
		}
		w.delivered = &deliveries{}
		w.routing = &telegram.Routing{To: w.delivered}
		w.opts.Router = w.routing
		w.opts.Commands = append(append([]telegram.Command(nil), w.opts.Commands...), w.routing.Commands()...)
		acts := map[string]telegram.Action{}
		for k, v := range w.opts.Actions {
			acts[k] = v
		}
		for k, v := range w.routing.Actions() {
			acts[k] = v
		}
		w.opts.Actions = acts
	}
	if ropt.block {
		for _, slug := range []string{"loja", "blog"} {
			if _, err := db.Writer().ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
				VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
				t.Fatal(err)
			}
		}
		w.bc = &telegram.BlockChannel{Users: w.users, PanelURL: "https://painel.test/cofre"}
		w.cc = &telegram.ChooseChannel{Users: w.users, Answered: func(_ context.Context, q telegram.ChoiceQuestion, by users.User, option string) error {
			w.answers = append(w.answers, telegram.AnswerItem(q, by, option))
			return nil
		}}
		acts := map[string]telegram.Action{}
		for k, v := range w.opts.Actions {
			acts[k] = v
		}
		for k, v := range w.bc.Actions() {
			acts[k] = v
		}
		for k, v := range w.cc.Actions() {
			acts[k] = v
		}
		w.pauser = &fakeTablePauser{}
		w.sc = &telegram.StallChannel{Users: w.users, Pauser: w.pauser}
		for k, v := range w.sc.Actions() {
			acts[k] = v
		}
		w.opts.Actions = acts
	}
	if ropt.decrees {
		for _, slug := range []string{"loja", "blog"} {
			if _, err := db.Writer().ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
				VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
				t.Fatal(err)
			}
		}
		var alerts users.Alerts = w.alerts
		if w.oa != nil {
			alerts = w.oa
		}
		w.requests = requests{}
		w.ds, err = decrees.New(decrees.Options{DB: db, Clock: clk, PINs: w.users, Alerts: alerts, Requests: w.requests})
		if err != nil {
			t.Fatal(err)
		}
		w.rules, err = tablerules.NewService(tablerules.Options{Store: &tablerules.Store{DB: db}, Clock: clk, Characters: struct{ chain.Characters }{}, Policy: chain.StaticPolicy{}, Classes: struct{ chain.Classes }{}})
		if err != nil {
			t.Fatal(err)
		}
		pins := &telegram.PINRouter{Users: w.users}
		w.dc = &telegram.DecreeChannel{Svc: w.ds, Users: w.users, PINs: pins, Rules: telegram.TableRulesOf(w.rules)}
		w.opts.Commands = append(append([]telegram.Command(nil), w.opts.Commands...), w.dc.Commands()...)
		w.opts.Actions = maps.Clone(w.opts.Actions) // the actions of the other parts stay
		if w.opts.Actions == nil {
			w.opts.Actions = map[string]telegram.Action{}
		}
		for k, v := range ropt.actions {
			w.opts.Actions[k] = v
		}
		for k, v := range w.dc.Actions() {
			w.opts.Actions[k] = v
		}
		w.opts.Captures = map[string]telegram.CaptureFunc{telegram.PINKind: pins.Capture()}
		if ropt.digest {
			w.dg = &telegram.DigestChannel{Users: w.users, Decrees: w.dc}
			for k, v := range w.dg.Actions() {
				w.opts.Actions[k] = v
			}
		}
	}
	if ropt.guild {
		pins := &telegram.PINRouter{Users: w.users}
		w.gf = &guildFake{machines: []telegram.GuildMachine{{ID: "m-a", Name: "casa", Citadel: true}, {ID: "m-b", Name: "notebook"}}}
		w.gc = &telegram.GuildChannel{Users: w.users, PINs: pins, Machines: w.gf.list, SetExcluded: w.gf.set, Pending: w.gf.waiting}
		w.opts.Commands = append(append([]telegram.Command(nil), w.opts.Commands...), w.gc.Commands()...)
		acts := map[string]telegram.Action{}
		for k, v := range w.opts.Actions {
			acts[k] = v
		}
		for k, v := range w.gc.Actions() {
			acts[k] = v
		}
		w.opts.Actions = acts
		w.opts.Captures = map[string]telegram.CaptureFunc{telegram.PINKind: pins.Capture()}
	}
	w.bot, err = telegram.New(w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if w.oa != nil {
		w.oa.Bind(w.bot)
	}
	if w.dc != nil {
		w.dc.Bind(w.bot)
	}
	if w.gc != nil {
		w.gc.Bind(w.bot)
	}
	if w.dg != nil {
		w.dg.Bind(w.bot)
	}
	if w.routing != nil {
		w.routing.Bind(w.bot)
	}
	if w.bc != nil {
		w.bc.Bind(w.bot)
		w.sc.Bind(w.bot)
	}
	if w.cc != nil {
		w.cc.Bind(w.bot)
	}
	if w.lc != nil {
		w.lc.Bind(w.bot)
		w.lock.OnChange(func(locked bool) {
			if !locked {
				if err := w.bot.ReleaseHeld(w.ctx); err != nil {
					t.Error(err)
				}
			}
		})
	}
	if ropt.approvals {
		w.chain = fakechannel.NewChain(db, clk)
		w.flow, err = approvals.New(approvals.Options{DB: db, Clock: clk, Chain: w.chain, Users: w.users, PINs: w.users, Messenger: w.ac,
			Locked: func(context.Context) bool { return false }})
		if err != nil {
			t.Fatal(err)
		}
		w.chain.Flow = w.flow
		w.ac.Bind(w.bot, w.flow)
	}
	return w
}

// user registers and pairs a user.
func (w *world) user(name string, level users.Level, workspaces ...string) (users.User, faketelegram.User) {
	w.t.Helper()
	u, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: workspaces})
	if err != nil {
		w.t.Fatal(err)
	}
	w.next++
	c, err := w.users.NewCode(w.ctx, w.owner, u.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	if u, err = w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: w.next, Private: true}); err != nil {
		w.t.Fatal(err)
	}
	return u, faketelegram.User{ID: w.next, FirstName: name, Username: strings.ToLower(name)}
}

func (w *world) poll() {
	w.t.Helper()
	if err := w.bot.PollOnce(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) flush() time.Time {
	w.t.Helper()
	next, err := w.bot.Flush(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return next
}

// say makes the user send a text and the bot read it.
func (w *world) say(u faketelegram.User, text string) {
	w.t.Helper()
	if _, err := w.fake.UserSendsText(u, faketelegram.Private(u), text); err != nil {
		w.t.Fatal(err)
	}
	w.poll()
}

// sent are the messages of a method the Telegram received, in order.
func (w *world) sent(method string) []faketelegram.Sent {
	var out []faketelegram.Sent
	for _, s := range w.fake.Sent() {
		if s.Method == method {
			out = append(out, s)
		}
	}
	return out
}

// tell sends a notice to a user, as the engine does for the lock.
func (w *world) tell(ctx context.Context, id, text string) {
	u, err := w.users.Get(ctx, id)
	if err != nil || u.TelegramID == 0 {
		return
	}
	if _, err := w.bot.Send(ctx, telegram.Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
		w.t.Error(err)
	}
}

// deliveries records what the routing hands to the Grand Masters.
type deliveries struct {
	mu  sync.Mutex
	got []telegram.Delivery
}

func (d *deliveries) Deliver(_ context.Context, m telegram.Delivery) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, m)
	return nil
}

func (d *deliveries) all() []telegram.Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]telegram.Delivery(nil), d.got...)
}
