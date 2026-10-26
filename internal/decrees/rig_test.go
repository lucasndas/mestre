package decrees_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const correctPIN = "482913"

// alerts records what the people are told.
type alerts struct {
	mu    sync.Mutex
	tells map[string][]string
	owner []string
}

func (a *alerts) Unauthorized(context.Context, int64, string) {}

func (a *alerts) OwnerAlert(_ context.Context, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owner = append(a.owner, text)
}

func (a *alerts) Tell(_ context.Context, user, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tells == nil {
		a.tells = map[string][]string{}
	}
	a.tells[user] = append(a.tells[user], text)
}

func (a *alerts) toldTo(user string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.tells[user]...)
}

func (a *alerts) ownerTold() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.owner...)
}

// heads is the live head of the PRs.
type heads struct {
	mu  sync.Mutex
	cur map[string]string
}

func (h *heads) set(repo string, pr int, head string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur == nil {
		h.cur = map[string]string{}
	}
	h.cur[fmt.Sprintf("%s#%d", repo, pr)] = head
}

func (h *heads) Current(_ context.Context, r chain.Request) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.PR == nil {
		return nil, fmt.Errorf("no PR")
	}
	head := h.cur[fmt.Sprintf("%s#%d", r.PR.Repo, r.PR.Number)]
	return json.RawMessage(fmt.Sprintf(`{"repo":%q,"pr":%d,"head":%q,"base":"main"}`, r.PR.Repo, r.PR.Number, head)), nil
}

// requests are the approval requests, by id.
type requests map[int64]chain.Request

func (r requests) Get(_ context.Context, id int64) (chain.Request, error) {
	if q, ok := r[id]; ok {
		return q, nil
	}
	return chain.Request{}, fmt.Errorf("no request %d", id)
}

type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	users *users.Service
	svc   *decrees.Service
	owner users.User
	al    *alerts
	heads *heads
	reqs  requests
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
	for _, slug := range []string{"loja", "blog"} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, slug, "lucas/"+slug, "lucas/"+slug); err != nil {
			t.Fatal(err)
		}
	}
	w := &world{t: t, ctx: ctx, clk: clk, db: db, al: &alerts{}, heads: &heads{}, reqs: requests{}}
	w.users, err = users.New(users.Options{DB: db, Clock: clk, Alerts: w.al, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if w.owner, err = w.users.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	if err := w.users.SetPIN(ctx, w.owner.ID, correctPIN); err != nil {
		t.Fatal(err)
	}
	w.svc, err = decrees.New(decrees.Options{DB: db, Clock: clk, PINs: w.users, Alerts: w.al, Targets: w.heads, Requests: w.reqs})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// person registers a user with a PIN.
func (w *world) person(name string, level users.Level, workspaces ...string) users.User {
	w.t.Helper()
	u, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: workspaces})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.SetPIN(w.ctx, u.ID, correctPIN); err != nil {
		w.t.Fatal(err)
	}
	return u
}

// pin answers a request of PIN right for the user and returns its id.
func (w *world) pin(u users.User, action string) string {
	w.t.Helper()
	req, err := w.users.StartPIN(w.ctx, u.ID, action)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, req.ID, correctPIN); err != nil {
		w.t.Fatal(err)
	}
	return req.ID
}

// allowDev is the proposal "o Dev pode <comando>" of a rule.
func allowCmd(author users.User, class string, cmds ...string) decrees.Proposal {
	return decrees.Proposal{Author: author.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetClass, Name: class},
		Effect: decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: cmds}},
		Text:   "permita " + strings.Join(cmds, ", ") + " à classe " + class, Origin: decrees.Origin{Channel: "telegram", MessageID: 7}}
}

func denyCmd(author users.User, class string, cmds ...string) decrees.Proposal {
	p := allowCmd(author, class, cmds...)
	p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Deny: &classes.Grant{Commands: cmds}}
	return p
}

// confirm proposes and confirms, giving the PIN when the engine asks for it.
func (w *world) confirm(p decrees.Proposal) decrees.Decree {
	w.t.Helper()
	u, err := w.users.Get(w.ctx, p.Author)
	if err != nil {
		w.t.Fatal(err)
	}
	pend, err := w.svc.Propose(w.ctx, p)
	if err != nil {
		w.t.Fatalf("propose: %v", err)
	}
	pinID := ""
	if pend.PIN {
		req, err := w.svc.BeginPIN(w.ctx, pend.ID, u.ID)
		if err != nil {
			w.t.Fatal(err)
		}
		if err := w.users.CheckPIN(w.ctx, req.ID, correctPIN); err != nil {
			w.t.Fatal(err)
		}
		pinID = req.ID
	}
	d, err := w.svc.Confirm(w.ctx, pend.ID, u.ID, pinID)
	if err != nil {
		w.t.Fatalf("confirm: %v", err)
	}
	return d
}

func (w *world) refusal(p decrees.Proposal) *decrees.Refusal {
	w.t.Helper()
	_, err := w.svc.Propose(w.ctx, p)
	ref, ok := err.(*decrees.Refusal)
	if !ok {
		w.t.Fatalf("propose: %v; want a refusal", err)
	}
	return ref
}

func (w *world) count(table string) int {
	w.t.Helper()
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// dev is a character of the class dev in the workspace loja.
func dev(id string) characters.Character {
	return characters.Character{ID: id, Name: id, Role: characters.RoleCharacter, Workspace: "loja", Table: "42-login", Class: "dev"}
}

func shell(cmd string) classes.Action {
	return classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: cmd}
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%q does not contain %q", got, want)
	}
}
