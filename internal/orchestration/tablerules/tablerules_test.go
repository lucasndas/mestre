package tablerules_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fakeChars is the registry of the characters: a Mestre da mesa, a Grão-Mestre
// and a developer, with what the rules look at.
type fakeChars struct {
	mu    sync.Mutex
	chars map[string]characters.Character
	// sups maps a character to its superior (the people when absent).
	sups map[string]string
}

func (f *fakeChars) Get(_ context.Context, id string) (characters.Character, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.chars[id]
	if !ok {
		return characters.Character{}, characters.ErrNoCharacter
	}
	return c, nil
}

func (f *fakeChars) Superior(_ context.Context, id string) (characters.Superior, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if up, ok := f.sups[id]; ok {
		return characters.Superior{ID: up}, nil
	}
	return characters.Superior{People: true}, nil
}

func (f *fakeChars) find(role characters.Role, table string) (characters.Character, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.chars {
		if c.Role == role && c.Table == table {
			return c, true
		}
	}
	return characters.Character{}, false
}

func (f *fakeChars) TableMaster(_ context.Context, _, table string) (characters.Character, bool, error) {
	c, ok := f.find(characters.RoleTableMaster, table)
	return c, ok, nil
}

func (f *fakeChars) GrandMaster(context.Context, string) (characters.Character, bool, error) {
	c, ok := f.find(characters.RoleGrandMaster, "")
	return c, ok, nil
}

func (f *fakeChars) Relabel(context.Context, string, string) error { return nil }

func (f *fakeChars) set(c characters.Character) {
	f.mu.Lock()
	f.chars[c.ID] = c
	f.mu.Unlock()
}

type fakeMissions struct {
	mu     sync.Mutex
	active map[string]string
	over   map[string]bool
}

func (m *fakeMissions) Active(_ context.Context, c string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[c], nil
}

func (m *fakeMissions) Over(_ context.Context, q string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.over[q], nil
}

type fakeTables struct {
	mu   sync.Mutex
	over map[string]bool
}

func (t *fakeTables) Over(_ context.Context, _, table string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.over[table], nil
}

type fakeInbox struct {
	mu    sync.Mutex
	items []inbox.NewItem
}

func (i *fakeInbox) Enqueue(_ context.Context, n inbox.NewItem) (inbox.Item, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.items = append(i.items, n)
	return inbox.Item{}, nil
}
func (i *fakeInbox) Cancel(context.Context, string, inbox.State) error { return nil }
func (i *fakeInbox) Pump(context.Context, string) error                { return nil }

func (i *fakeInbox) toldTo(id string) []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []string
	for _, n := range i.items {
		if n.Target == id {
			out = append(out, n.Text)
		}
	}
	return out
}

// fakeClasses gives each character the rules of its YAML (every command asks
// when none was set).
type fakeClasses struct {
	mu   sync.Mutex
	yaml map[string]string
}

func (f *fakeClasses) setYAML(id, y string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.yaml[id] = y
}

func (f *fakeClasses) Rules(_ context.Context, id string) (*classes.Rules, error) {
	f.mu.Lock()
	y, ok := f.yaml[id]
	f.mu.Unlock()
	if !ok {
		y = "version: 1\ncommands:\n  default: ask\n"
	}
	p, err := classes.ParsePermissions([]byte(y))
	if err != nil {
		return nil, err
	}
	return classes.Build(classes.Layers{AgentID: id, Class: "dev", Permissions: p, Now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)})
}
func (*fakeClasses) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, Worktree: "/work/42-login", OS: "linux"}, nil
}
func (*fakeClasses) Domains(context.Context, string) ([]string, error) {
	return []string{"pypi.org"}, nil
}

type rig struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	svc      *tablerules.Service
	chars    *fakeChars
	classes  *fakeClasses
	missions *fakeMissions
	tables   *fakeTables
	inbox    *fakeInbox
	master   characters.Character
	gm       characters.Character
	dev      characters.Character
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &rig{t: t, ctx: ctx, clk: clk, chars: &fakeChars{chars: map[string]characters.Character{}}, classes: &fakeClasses{yaml: map[string]string{}}, missions: &fakeMissions{active: map[string]string{}, over: map[string]bool{}},
		tables: &fakeTables{over: map[string]bool{}}, inbox: &fakeInbox{}}
	r.master = characters.Character{ID: "m1", Name: "Mestre", Role: characters.RoleTableMaster, Workspace: "meu-app", Table: "42-login", State: characters.Idle}
	r.gm = characters.Character{ID: "g1", Name: "Grão-Mestre", Role: characters.RoleGrandMaster, Workspace: "meu-app", State: characters.Idle}
	r.dev = characters.Character{ID: "d1", Name: "Dev", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "Dev", State: characters.Idle}
	for _, c := range []characters.Character{r.master, r.gm, r.dev} {
		r.chars.set(c)
	}
	r.svc, err = tablerules.NewService(tablerules.Options{Store: &tablerules.Store{DB: db}, Clock: clk, Characters: r.chars, Policy: chain.StaticPolicy{}, Classes: r.classes,
		Missions: r.missions, Tables: r.tables, Inbox: r.inbox})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) pattern(p string, risk classes.Risk) *classes.ApprovablePattern {
	return &classes.ApprovablePattern{Pattern: p, Kind: providers.KindShell, Risk: classes.RiskResult{Risk: risk}}
}

// make creates the rule of a pattern of the Mestre da mesa.
func (r *rig) make(p string, d time.Duration, mission bool) tablerules.Rule {
	r.t.Helper()
	err := r.svc.Create(r.ctx, chain.TempRule{Request: 7, Workspace: "meu-app", Table: "42-login", Class: "Dev", Requester: r.dev.ID, Pattern: r.pattern(p, classes.RiskLow),
		Action: classes.Action{Kind: providers.KindShell, Command: "npm install x"}, Risk: classes.RiskLow, Until: r.clk.Now().Add(d), UntilMissionEnd: mission, By: r.master.ID,
		Level: chain.Level{Kind: chain.LevelMesa, Agent: r.master.ID}})
	if err != nil {
		r.t.Fatal(err)
	}
	rules, err := r.svc.List(r.ctx, "meu-app", "42-login")
	if err != nil || len(rules) == 0 {
		r.t.Fatalf("rules = %+v, %v", rules, err)
	}
	return rules[len(rules)-1]
}

func (r *rig) use(id int64, cmd string) bool {
	r.t.Helper()
	ok, err := r.svc.Use(r.ctx, classes.TableRuleUse{Rule: id, Level: classes.LevelTableMaster, Risk: classes.RiskLow, AgentID: r.dev.ID, TableID: "42-login",
		Action: classes.Action{Kind: providers.KindShell, Command: cmd}, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: cmd}, Own: classes.RiskResult{Risk: classes.RiskLow}})
	if err != nil {
		r.t.Fatal(err)
	}
	return ok
}

func (r *rig) state(id int64) tablerules.State {
	r.t.Helper()
	x, err := r.svc.Get(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return x.State
}

// CA-RTM-04
func TestARuleUntilTheEndOfTheMissionExpiresWithItOrWithTheTableAndAnotherIsRevokedByTheGrandMaster(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.missions.active[r.dev.ID] = "q1"
	byMission := r.make("npm install *", 20*time.Hour, true)
	if byMission.Mission != "q1" {
		t.Fatalf("rule = %+v, want the mission of who asked", byMission)
	}
	if !r.use(byMission.ID, "npm install y") {
		t.Fatal("the rule does not let the action through while the mission goes on")
	}
	r.missions.mu.Lock()
	r.missions.over["q1"] = true
	r.missions.mu.Unlock()
	if r.use(byMission.ID, "npm install y") {
		t.Error("the gate let an action through after the mission ended, before the sweep")
	}
	if err := r.svc.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.state(byMission.ID); got != tablerules.Expired {
		t.Errorf("state after the mission = %s, want expired", got)
	}
	if told := r.inbox.toldTo(r.master.ID); len(told) != 1 || !strings.Contains(told[0], "#"+itoa(byMission.ID)) || !strings.Contains(told[0], "missão") {
		t.Errorf("the author was told %q", told)
	}

	// the end of the table
	byTable := r.make("npm install *", 2*time.Hour, false)
	r.tables.mu.Lock()
	r.tables.over["42-login"] = true
	r.tables.mu.Unlock()
	if err := r.svc.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.state(byTable.ID); got != tablerules.Expired {
		t.Errorf("state after the table ended = %s, want expired", got)
	}
	r.tables.mu.Lock()
	r.tables.over["42-login"] = false
	r.tables.mu.Unlock()

	// the Grand Master revokes another: the next action asks again
	other := r.make("npm install *", 2*time.Hour, false)
	if !r.use(other.ID, "npm install y") {
		t.Fatal("the rule does not let the action through")
	}
	if err := r.svc.Revoke(r.ctx, other.ID, tablerules.Revoker{Character: &r.gm}); err != nil {
		t.Fatal(err)
	}
	if got := r.state(other.ID); got != tablerules.Revoked {
		t.Errorf("state after the revocation = %s, want revoked", got)
	}
	if r.use(other.ID, "npm install y") {
		t.Error("a revoked rule let an action through")
	}
	if ds, _ := r.svc.Decrees(r.ctx, "meu-app", "42-login", "Dev"); len(ds) != 0 {
		t.Errorf("decrees = %+v after the revocation", ds)
	}
	if told := r.inbox.toldTo(r.master.ID); len(told) != 3 || !strings.Contains(told[2], "revogada") {
		t.Errorf("the author was told %q", told)
	}
	if err := r.svc.Revoke(r.ctx, other.ID, tablerules.Revoker{Character: &r.gm}); err == nil {
		t.Error("a rule revoked twice")
	}
	hist, err := r.svc.History(r.ctx, other.ID)
	if err != nil || len(hist) != 2 || hist[1].State != tablerules.Revoked {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{digits[n%10]}, b...)
	}
	return string(b)
}

// Part of CA-RTM-04: who may revoke, the time itself, the author leaving its
// place, and the lock of the system.
func TestOnlyTheAuthorTheGrandMasterOrAnAdministratorRevokeAndAnAuthorWhoLeavesEndsTheRule(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.make("npm install *", time.Hour, false)
	if err := r.svc.Revoke(r.ctx, a.ID, tablerules.Revoker{Character: &r.dev}); err != tablerules.ErrNotAllowed {
		t.Errorf("a developer revoking = %v, want not allowed", err)
	}
	if err := r.svc.Revoke(r.ctx, a.ID, tablerules.Revoker{Administrator: true, ID: "u1"}); err != nil {
		t.Errorf("an Administrator revoking = %v", err)
	}
	if err := r.svc.Revoke(r.ctx, 999, tablerules.Revoker{Administrator: true}); err != tablerules.ErrNoRule {
		t.Errorf("revoking nothing = %v", err)
	}

	b := r.make("npm install *", time.Hour, false)
	if err := r.svc.Revoke(r.ctx, b.ID, tablerules.Revoker{Character: &r.master}); err != nil {
		t.Errorf("the author revoking = %v", err)
	}

	// the time itself
	c := r.make("npm install *", time.Hour, false)
	r.clk.Advance(time.Hour)
	if r.use(c.ID, "npm install y") {
		t.Error("the gate let an action through after the validity")
	}
	if err := r.svc.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.state(c.ID); got != tablerules.Expired {
		t.Errorf("state after the validity = %s", got)
	}

	// the author stops being the Mestre of the table
	d := r.make("npm install *", time.Hour, false)
	other := characters.Character{ID: "m2", Name: "Outro", Role: characters.RoleTableMaster, Workspace: "meu-app", Table: "42-login", State: characters.Idle}
	r.chars.mu.Lock()
	delete(r.chars.chars, r.master.ID)
	r.chars.mu.Unlock()
	r.chars.set(other)
	if r.use(d.ID, "npm install y") {
		t.Error("the gate let the rule of a Mestre that was replaced through")
	}
	if err := r.svc.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.state(d.ID); got != tablerules.Expired {
		t.Errorf("state after the author left = %s", got)
	}

	// the lock of the system suspends and the unlock brings the rule back
	r.chars.set(r.master)
	r.chars.mu.Lock()
	delete(r.chars.chars, other.ID)
	r.chars.mu.Unlock()
	e := r.make("npm install *", time.Hour, false)
	if err := r.svc.Suspend(r.ctx, "meu-app", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := r.state(e.ID); got != tablerules.Suspended || r.use(e.ID, "npm install y") {
		t.Errorf("suspended rule: state %s", got)
	}
	if ds, _ := r.svc.Decrees(r.ctx, "meu-app", "42-login", "Dev"); len(ds) != 0 {
		t.Errorf("decrees = %+v while the system is locked", ds)
	}
	if err := r.svc.Resume(r.ctx, "meu-app", "owner"); err != nil {
		t.Fatal(err)
	}
	if !r.use(e.ID, "npm install y") {
		t.Error("the rule does not hold after the unlock")
	}
	if n, err := r.svc.RevokeAll(r.ctx, "meu-app", "owner"); err != nil || n != 1 {
		t.Errorf("revoke all = %d, %v", n, err)
	}
}

// A rule is for the class and the table it was made in, and a mission rule
// needs a mission.
func TestARuleServesOnlyTheClassAndTheTableItWasMadeForAndAMissionRuleNeedsAMission(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.make("npm install *", time.Hour, false)
	for name, c := range map[string]characters.Character{
		"another class": {ID: "d2", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "QA", State: characters.Idle},
		"another table": {ID: "d3", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "43-outra", Class: "Dev", State: characters.Idle},
	} {
		r.chars.set(c)
		ok, err := r.svc.Use(r.ctx, classes.TableRuleUse{Rule: a.ID, AgentID: c.ID, Action: classes.Action{Kind: providers.KindShell, Command: "npm install y"}, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm install y"}, Own: classes.RiskResult{Risk: classes.RiskLow}})
		if err != nil || ok {
			t.Errorf("%s: use = %v, %v", name, ok, err)
		}
	}
	err := r.svc.Create(r.ctx, chain.TempRule{Workspace: "meu-app", Table: "42-login", Class: "Dev", Requester: r.dev.ID, Pattern: r.pattern("npm install *", classes.RiskLow), Risk: classes.RiskLow,
		Until: r.clk.Now().Add(time.Hour), UntilMissionEnd: true, By: r.master.ID, Level: chain.Level{Kind: chain.LevelMesa}})
	if err != chain.ErrNoMission {
		t.Errorf("a mission rule with no mission = %v, want ErrNoMission", err)
	}
	if err := r.svc.Create(r.ctx, chain.TempRule{Workspace: "meu-app", Table: "42-login", Class: "Dev", Risk: classes.RiskHigh, Until: r.clk.Now().Add(time.Hour), By: r.master.ID}); err == nil {
		t.Error("a rule of alto was made")
	}
}

// The author of a rule approves only what its own rules would not deny, and the
// gate checks it on the call that uses the rule: a pattern reaches calls its
// author never saw, and a call its author's rules deny is not freed by it.
func TestAPatternRuleDoesNotFreeWhatTheRulesOfItsAuthorDeny(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	rule := r.make("npm install *", time.Hour, false)
	if !r.use(rule.ID, "npm install lodash") {
		t.Fatal("the rule does not free an action its author's rules allow")
	}
	r.classes.setYAML(r.master.ID, "version: 1\ncommands:\n  deny: [\"npm install evil*\"]\n  default: ask\n")
	if r.use(rule.ID, "npm install evil-pkg") {
		t.Error("the pattern freed an action that the rules of its author deny")
	}
	if !r.use(rule.ID, "npm install lodash") {
		t.Error("the pattern stopped freeing what its author's rules allow")
	}
}

// The baixo rule of a superior that is an agent covers its own subordinates,
// not every character of the class in the table.
func TestTheRuleOfASuperiorAgentCoversOnlyItsSubordinates(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	boss := characters.Character{ID: "a1", Name: "Chefe", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "Dev", State: characters.Idle}
	mine := characters.Character{ID: "d2", Name: "Meu", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "Dev", State: characters.Idle}
	r.chars.set(boss)
	r.chars.set(mine)
	r.chars.mu.Lock()
	r.chars.sups = map[string]string{"d2": "a1", "d1": "m1"}
	r.chars.mu.Unlock()
	err := r.svc.Create(r.ctx, chain.TempRule{Request: 8, Workspace: "meu-app", Table: "42-login", Class: "Dev", Requester: mine.ID, Pattern: r.pattern("npm install *", classes.RiskLow),
		Action: classes.Action{Kind: providers.KindShell, Command: "npm install x"}, Risk: classes.RiskLow, Until: r.clk.Now().Add(time.Hour), By: boss.ID,
		Level: chain.Level{Kind: chain.LevelAgent, Agent: boss.ID}})
	if err != nil {
		t.Fatal(err)
	}
	rules, err := r.svc.List(r.ctx, "meu-app", "42-login")
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	call := func(agent string) bool {
		ok, err := r.svc.Use(r.ctx, classes.TableRuleUse{Rule: rules[0].ID, Level: classes.LevelTableMaster, Risk: classes.RiskLow, AgentID: agent, TableID: "42-login",
			Action: classes.Action{Kind: providers.KindShell, Command: "npm install y"}, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm install y"},
			Own: classes.RiskResult{Risk: classes.RiskLow}})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !call(mine.ID) {
		t.Error("the rule does not free a subordinate of its author")
	}
	if call(r.dev.ID) {
		t.Error("the rule of a superior agent freed a character that does not answer to it")
	}
}
