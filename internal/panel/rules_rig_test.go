package panel_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/panel"
)

// fakeEngine is the part of the engine behind the screens of the chain that is not the
// decrees: the temporary table rules and the supervision of the tables. What they decide
// (the escalation of the open requests, the lock of a rule) is proved by their own
// criteria (CA-SUPCAD-02, CA-TEMP-*); here only what the panel asks of them and what it
// shows. The decrees are the real service.
type fakeEngine struct {
	*decrees.Service
	clk interface{ Now() time.Time }

	mu     sync.Mutex
	rules  map[int64]*tablerules.Rule
	pauses map[string]chain.Pause
	// opened is when each open request of the table was born.
	opened map[string][]time.Time
	// asked is what the panel asked the supervision, in order.
	asked []chain.PauseRequest
	dir   string
}

func (f *fakeEngine) LiveRules(_ context.Context, ws string) ([]tablerules.Rule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tablerules.Rule
	for _, r := range f.rules {
		if r.Workspace == ws && !r.State.Ended() {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeEngine) Rule(_ context.Context, id int64) (tablerules.Rule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rules[id]
	if !ok {
		return tablerules.Rule{}, tablerules.ErrNoRule
	}
	return *r, nil
}

func (f *fakeEngine) RuleUses(context.Context, int64) ([]tablerules.Use, error) { return nil, nil }

func (f *fakeEngine) RevokeRule(_ context.Context, id int64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rules[id]
	if !ok {
		return tablerules.ErrNoRule
	}
	if r.State.Ended() {
		return tablerules.ErrEnded
	}
	r.State = tablerules.Revoked
	return nil
}

// RevokeRules is what the emergency button reaches (decrees.RevokeHooks).
func (f *fakeEngine) RevokeRules(_ context.Context, ws, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rules {
		if r.Workspace == ws && !r.State.Ended() {
			r.State = tablerules.Revoked
			n++
		}
	}
	return n, nil
}
func (f *fakeEngine) Reroute(context.Context, string) error { return nil }
func (f *fakeEngine) Told(context.Context, string, string)  {}

func (f *fakeEngine) OpenTables(context.Context, string) ([]string, error) {
	return []string{"42-login", "43-busca"}, nil
}

func (f *fakeEngine) Metrics(_ context.Context, ws string) ([]chain.QueueMetric, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.clk.Now()
	var out []chain.QueueMetric
	for table, times := range f.opened {
		if len(times) == 0 {
			continue
		}
		oldest := time.Duration(0)
		for _, t := range times {
			oldest = max(oldest, now.Sub(t))
		}
		out = append(out, chain.QueueMetric{Workspace: ws, Level: chain.LevelMesa, Table: table, Open: len(times), Oldest: oldest})
	}
	out = append(out, chain.QueueMetric{Workspace: ws, Level: chain.LevelPeople, Open: 1, Oldest: 5 * time.Minute})
	return out, nil
}

func (f *fakeEngine) PauseOf(_ context.Context, ws, table string) (chain.Pause, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pauses[ws+"/"+table]
	return p, ok, nil
}

func (f *fakeEngine) PauseAutonomy(_ context.Context, r chain.PauseRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, r)
	if !approverOrAbove(r.By) {
		return false, chain.ErrLevelDenied
	}
	k := r.Workspace + "/" + r.Table
	if _, ok := f.pauses[k]; ok {
		return false, nil
	}
	f.pauses[k] = chain.Pause{Workspace: r.Workspace, Table: r.Table, By: "user:" + r.By.User, Kind: "person", Reason: r.Reason, Since: f.clk.Now()}
	return true, nil
}

func (f *fakeEngine) ResumeAutonomy(_ context.Context, r chain.PauseRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, r)
	if !approverOrAbove(r.By) {
		return false, chain.ErrLevelDenied
	}
	k := r.Workspace + "/" + r.Table
	p, ok := f.pauses[k]
	if !ok {
		return false, nil
	}
	if r.RefuseCharacterPause && p.Kind == "character" {
		return false, chain.ErrCharacterPause
	}
	delete(f.pauses, k)
	return true, nil
}

func approverOrAbove(a chain.Actor) bool {
	switch a.UserLevel {
	case "approver", "administrator", "owner":
		return true
	}
	return false
}

func (f *fakeEngine) Principal(ws string) string {
	if ws != "loja" {
		return ""
	}
	return f.dir
}

func (f *fakeEngine) asking() []chain.PauseRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chain.PauseRequest(nil), f.asked...)
}

// rulesRig is the admin rig with the real decrees and the fake rest of the engine.
type rulesRig struct {
	*adminRig
	eng *fakeEngine
	svc *decrees.Service
}

func newRules(t *testing.T) *rulesRig {
	t.Helper()
	a := newAdmin(t)
	svc, err := decrees.New(decrees.Options{DB: a.db, Clock: a.clk, PINs: a.users, Alerts: a.al})
	if err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{Service: svc, clk: a.clk, rules: map[int64]*tablerules.Rule{}, pauses: map[string]chain.Pause{},
		opened: map[string][]time.Time{}, dir: filepath.Join(t.TempDir(), "principal")}
	svc.SetRevokeHooks(revokeHooks{eng})
	if err := os.MkdirAll(filepath.Join(eng.dir, ".mestre", "regras"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.panel.SetRules(eng)
	return &rulesRig{adminRig: a, eng: eng, svc: svc}
}

// revokeHooks adapts the fake to decrees.RevokeHooks.
type revokeHooks struct{ f *fakeEngine }

func (h revokeHooks) RevokeRules(ctx context.Context, ws, by string) (int, error) {
	return h.f.RevokeRules(ctx, ws, by)
}
func (h revokeHooks) Reroute(ctx context.Context, ws string) error { return h.f.Reroute(ctx, ws) }
func (h revokeHooks) Told(ctx context.Context, ws, text string)    { h.f.Told(ctx, ws, text) }

func (r *rulesRig) addRule(id int64, class string) {
	r.eng.mu.Lock()
	defer r.eng.mu.Unlock()
	r.eng.rules[id] = &tablerules.Rule{ID: id, Workspace: "loja", Table: "42-login", Class: class, State: tablerules.Active, Level: chain.LevelMesa,
		Author: "mesa-42", Risk: classes.RiskMedium, Pattern: "npm test*", CreatedAt: r.clk.Now(), Until: r.clk.Now().Add(2 * time.Hour)}
}

// delegation makes an active delegation of the Owner, by the same path a person takes: a
// proposal and the click with the PIN.
func (r *rulesRig) delegation(tok string) int64 {
	r.t.Helper()
	pend, err := r.svc.Propose(r.ctx, decrees.Proposal{Author: r.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login",
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Until: r.clk.Now().Add(10 * 24 * time.Hour), Text: "o Grão-Mestre pode decidir envio de workflows da mesa 42-login"})
	if err != nil {
		r.t.Fatal(err)
	}
	w := r.call("POST", "/api/decrees/proposals/"+pend.ID+"/confirm", nil, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 201 {
		r.t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	return decodeInto[panel.ConfirmedDecree](r.t, w).Number
}

func (r *rulesRig) addSub(origin int64) {
	r.t.Helper()
	_, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO subdelegations (workspace, mesa, request_type, grantor, grantee, origin, reason, state, created_at, until) VALUES ('loja', '42-login', 'envio_workflows', 'gm', 'mesa-42', ?, 'testes', 'active', ?, ?)`,
		origin, r.clk.Now().UnixMilli(), r.clk.Now().Add(5*24*time.Hour).UnixMilli())
	if err != nil {
		r.t.Fatal(err)
	}
}

// writeRule writes a file of .mestre/regras/ of the workspace.
func (r *rulesRig) writeRule(rel, text string) string {
	r.t.Helper()
	path := filepath.Join(r.eng.dir, ".mestre", "regras", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return path
}

func chainPause(table, by, kind string, since time.Time) chain.Pause {
	return chain.Pause{Workspace: "loja", Table: table, By: by, Kind: kind, Reason: "pausa", Since: since}
}
