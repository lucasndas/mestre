package approvals_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakechannel"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// targets is the live target of the requests, by id.
type targets struct {
	mu  sync.Mutex
	cur map[int64]string
	err error
	// on runs when a target is read (a user suspended while it is).
	on func()
}

func (t *targets) Current(_ context.Context, r chain.Request) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.on != nil {
		t.on()
	}
	if t.err != nil {
		return nil, t.err
	}
	return json.RawMessage(t.cur[r.ID]), nil
}

func (t *targets) set(id int64, v string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cur[id] = v
}

// late gives the users.Service the flow that needs it back.
type late struct{ f **approvals.Flow }

func (l late) InvalidateBy(ctx context.Context, id string) error { return (*l.f).InvalidateBy(ctx, id) }

type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	svc   *users.Service
	owner users.User
	ch    *fakechannel.Chain
	msg   *fakechannel.Channel
	tg    *targets
	flow  *approvals.Flow
	nextR int64
	db    *store.DB
	locks bool
	mu    sync.Mutex
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 14, 30, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, slug := range []string{"loja", "blog"} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
			t.Fatal(err)
		}
	}
	w := &world{t: t, ctx: ctx, clk: clk, db: db, msg: fakechannel.New(), tg: &targets{cur: map[int64]string{}}}
	w.ch = fakechannel.NewChain(db, clk)
	svc, err := users.New(users.Options{DB: db, Clock: clk, Approvals: late{&w.flow}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	w.svc = svc
	if w.owner, err = svc.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	w.flow, err = approvals.New(approvals.Options{DB: db, Clock: clk, Chain: w.ch, Users: svc, PINs: svc, Messenger: w.msg, Targets: w.tg,
		Locked: func(context.Context) bool { w.mu.Lock(); defer w.mu.Unlock(); return w.locks }})
	if err != nil {
		t.Fatal(err)
	}
	w.ch.Flow = w.flow
	return w
}

func (w *world) lock(v bool) { w.mu.Lock(); w.locks = v; w.mu.Unlock() }

func (w *world) add(name string, level users.Level, workspaces ...string) users.User {
	w.t.Helper()
	u, err := w.svc.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: workspaces})
	if err != nil {
		w.t.Fatalf("create %s: %v", name, err)
	}
	return u
}

// open puts a request in front of the people with the quorum and the minimum.
func (w *world) open(o openOpts) chain.Request {
	w.t.Helper()
	w.nextR++
	r := chain.Request{ID: w.nextR, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}, Requester: "c1"}
	if o.typ != "" {
		r.Type = o.typ
	}
	if o.late {
		r.Deadline = w.clk.Now().Add(-time.Second)
	}
	r.PR = o.pr
	if o.action != "" {
		// a permission of the class for a shell command of a table
		r.Type, r.Table, r.Class = classes.RequestClassPermission, "42-login", "dev"
		if o.table != "" {
			r.Table = o.table
		}
		if o.class != "" {
			r.Class = o.class
		}
		r.Action = &classes.Action{Kind: providers.KindShell, Command: o.action}
		r.Risk = classes.RiskResult{Risk: classes.RiskLow}
	}
	if o.target != "" {
		imp, err := chain.Impress(json.RawMessage(o.target))
		if err != nil {
			w.t.Fatal(err)
		}
		r.Target, r.Impression = json.RawMessage(o.target), imp
		w.tg.set(r.ID, o.target)
	}
	if err := w.ch.Add(w.ctx, r); err != nil {
		w.t.Fatal(err)
	}
	min := o.min
	if min == "" {
		min = classes.LevelApprover
	}
	q := chain.PeopleQuestion{Request: r, Text: "Aprovar o PR #7?", Approvals: max(o.quorum, 1), Require: o.require, MinLevel: min, PIN: o.pin,
		Answers: []chain.Answer{chain.AnswerApprove, chain.AnswerDeny, chain.AnswerDenyInstruction, chain.AnswerAskBack, chain.AnswerApproveAlways, chain.AnswerApprovePattern}}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		w.t.Fatal(err)
	}
	return r
}

type openOpts struct {
	typ     classes.RequestType
	quorum  int
	require []string
	min     classes.Level
	pin     bool
	target  string
	late    bool // the time of the level is over
	pr      *chain.PRRef
	// action is the shell command of a permission of the class (type, table and risk follow).
	action string
	// table and class replace the table 42-login and the class dev of an action.
	table, class string
}

func (w *world) answer(u users.User, request int64, a chain.Answer) (approvals.Reply, error) {
	return w.flow.Answer(w.ctx, u.ID, approvals.Input{Request: request, Answer: a})
}

func (w *world) state(id int64) chain.State { return w.ch.State(id) }

func (w *world) textTo(u users.User) string {
	w.t.Helper()
	ref, _, ok := w.msg.To(u.ID)
	if !ok {
		w.t.Fatalf("no message to %s", u.Name)
	}
	txt, _ := w.msg.Read(ref)
	return txt
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%q does not contain %q", got, want)
	}
}
