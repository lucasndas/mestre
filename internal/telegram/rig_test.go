package telegram_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/glossary"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/telegram"
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
	next   int64
}

type rigOpts struct {
	terms    bool
	commands []telegram.Command
	actions  map[string]telegram.Action
	captures map[string]telegram.CaptureFunc
	mask     func(string) string
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
	w.opts = telegram.Options{DB: db, Clock: clk, Client: &telegram.Client{Token: token, HTTP: &http.Client{Transport: fakenet.HandlerTransport(w.fake)}},
		Users: w.users, Alerts: w.alerts, Router: w.router, Commands: ropt.commands, Actions: ropt.actions, Captures: ropt.captures, Mask: ropt.mask, PollTimeout: time.Millisecond}
	if ropt.terms {
		w.opts.Terms = glossary.Terms
	}
	w.bot, err = telegram.New(w.opts)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// user registers and pairs a user.
func (w *world) user(name string, level users.Level) (users.User, faketelegram.User) {
	w.t.Helper()
	u, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: nil})
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
