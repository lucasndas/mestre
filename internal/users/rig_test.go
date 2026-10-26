package users_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// alerts records what the Service tells.
type alerts struct {
	mu           sync.Mutex
	unauthorized []string
	owner        []string
	told         map[string][]string
}

func (a *alerts) Unauthorized(_ context.Context, id int64, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unauthorized = append(a.unauthorized, reason)
}

func (a *alerts) OwnerAlert(_ context.Context, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owner = append(a.owner, text)
}

func (a *alerts) Tell(_ context.Context, user, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.told == nil {
		a.told = map[string][]string{}
	}
	a.told[user] = append(a.told[user], text)
}

// approvals records the users whose approvals were invalidated.
type approvals struct {
	mu  sync.Mutex
	ids []string
}

func (a *approvals) InvalidateBy(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = append(a.ids, id)
	return nil
}

type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	svc   *users.Service
	al    *alerts
	appr  *approvals
	owner users.User
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := &world{t: t, ctx: ctx, clk: clk, db: db, al: &alerts{}, appr: &approvals{}}
	w.svc, err = users.New(users.Options{DB: db, Clock: clk, Alerts: w.al, Approvals: w.appr, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}, Password: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if w.owner, err = w.svc.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"loja", "blog"} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// add registers a user as the Owner.
func (w *world) add(name string, level users.Level, workspaces ...string) users.User {
	w.t.Helper()
	u, err := w.svc.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: workspaces})
	if err != nil {
		w.t.Fatalf("create %s: %v", name, err)
	}
	return u
}

// pair registers the user's Telegram id through a code.
func (w *world) pair(u users.User, telegram int64) users.User {
	w.t.Helper()
	c, err := w.svc.NewCode(w.ctx, w.owner, u.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	p, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: telegram, Private: true})
	if err != nil {
		w.t.Fatalf("pair %s: %v", u.Name, err)
	}
	return p
}

// pin sets the PIN of a user.
func (w *world) pin(u users.User, pin string) {
	w.t.Helper()
	if err := w.svc.SetPIN(w.ctx, u.ID, pin); err != nil {
		w.t.Fatal(err)
	}
}
