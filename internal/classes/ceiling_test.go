package classes_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// docCeiling is the example of docs/classes/teto-do-workspace.md#arquivo.
const docCeiling = `version: 1
permissions:
  files:
    write: ["**"]
    deny: ["infra/**", ".github/workflows/**", "**/.env*", "**/*.pem", "**/*.key", "**/id_rsa*", "**/id_ed25519*", "**/id_ecdsa*", "**/.npmrc", "**/credentials*.json", "**/service-account*.json"]
  commands:
    ask: ["npm publish *"]
    deny: ["docker *", "terraform *"]
  git:
    request_push: false
    workflows: never
  network:
    internet: true
    domains: ["registry.npmjs.org", "github.com"]
  cli:
    allow: ["issue *", "pr comment", "pr reply", "pr status"]
  operator: false
cost:
  usd: {per_table: 50, per_day: 100}
  hours: {per_table: 20}
`

func ceiling(t testing.TB, yaml string) *classes.Ceiling {
	t.Helper()
	c, err := classes.ParseCeiling([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseCeilingReadsTheDocExampleStrictly(t *testing.T) {
	t.Parallel()
	c := ceiling(t, docCeiling)
	if c.Hash == "" || *c.Cost.USD.PerTable != 50 || c.Cost.Tokens != nil {
		t.Fatalf("ceiling = %+v", c)
	}
	for name, bad := range map[string]string{
		"no version":        "permissions: {}\n",
		"inner version":     "version: 1\npermissions: {version: 1}\n",
		"unknown field":     "version: 1\nlimits: {}\n",
		"invalid domain":    "version: 1\npermissions: {network: {domains: [\"Example.com\"]}}\n",
		"negative cost":     "version: 1\ncost: {usd: {per_day: -1}}\n",
		"fractional tokens": "version: 1\ncost: {tokens: {per_table: 1.5}}\n",
		"unknown unit":      "version: 1\ncost: {eur: {per_day: 1}}\n",
		"two documents":     "version: 1\n---\nversion: 1\n",
	} {
		if _, err := classes.ParseCeiling([]byte(bad)); !errors.Is(err, classes.ErrInvalidCeiling) {
			t.Errorf("%s: err = %v; want ErrInvalidCeiling", name, err)
		}
	}
}

// Part of CA-TETO-01 (the check; the save by the table master is in
// classpkg_test.go)
func TestCreatingAClassBeyondTheCeilingDomainsIsRefused(t *testing.T) {
	t.Parallel()
	c := ceiling(t, "version: 1\npermissions: {network: {domains: [\"github.com\"]}}\n")
	_, err := classes.ValidateClass([]byte("version: 1\nnetwork: {internet: true, domains: [\"example.com\"]}\n"), c)
	if !errors.Is(err, classes.ErrCeilingDenied) || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("err = %v; want ErrCeilingDenied citing example.com", err)
	}
	if _, err := classes.ValidateClass([]byte("version: 1\nnetwork: {internet: true, domains: [\"github.com\"]}\n"), c); err != nil {
		t.Fatalf("a class inside the ceiling: %v", err)
	}
}

// CA-TETO-02
func TestTheCeilingDenyDecidesWithItsReason(t *testing.T) {
	t.Parallel()
	c := ceiling(t, "version: 1\npermissions: {files: {write: [\"**\"], deny: [\"infra/**\"]}}\n")
	class := parse(t, "version: 1\nfiles: {write: [\"**\"]}\n")
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: class, Ceiling: c.Maximum(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	m := newMachine()
	want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("infra/main.tf"))), classes.Deny, classes.LayerCeiling, "teto do workspace")
	want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("src/main.go"))), classes.Allow, "")
}

// CA-TETO-05
func TestClassCheckShowsAClassAboveACeilingThatNowDeniesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k := newKeeper(t)
	class := parse(t, "version: 1\nfiles: {write: [\"src/**\"]}\n")
	before, err := k.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := classes.CheckClass("dev", class, before); got.Above() {
		t.Fatalf("before the change: %s", got)
	}
	restricted := "version: 1\npermissions: {files: {deny: [\"src/**\", \"**/.env*\", \"**/*.pem\", \"**/*.key\", \"**/id_rsa*\", \"**/id_ed25519*\", \"**/id_ecdsa*\", \"**/.npmrc\", \"**/credentials*.json\", \"**/service-account*.json\"]}, git: {request_push: false, workflows: never}, operator: false}\n"
	if err := k.Change(ctx, []byte(restricted), classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}); err != nil {
		t.Fatal(err)
	}
	after, err := k.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := classes.CheckClass("dev", class, after)
	if got.Status() != "acima do teto" || !strings.Contains(got.String(), "acima do teto") || !strings.Contains(got.String(), "src/**") {
		t.Fatalf("class check = %q; want the class above the ceiling, citing src/**", got)
	}
}

// Part of CA-TETO-06: the cut of operator by the default ceiling and the
// refusal of an Administrator turning it on, which the decree flow asks
// before the confirmation. The criterion closes in M7, with the Operador do
// PC and the ceiling decree (docs/desenvolvimento/ordem-de-implementacao.md#operador-do-pc-no-m7).
func TestTheDefaultCeilingTurnsOperatorOffAndOnlyTheOwnerTurnsItOn(t *testing.T) {
	t.Parallel()
	class := parse(t, "version: 1\noperator: true\n")
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "op", Permissions: class, Ceiling: classes.DefaultCeiling().Maximum(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if r.Operator || r.Origin["operator"] != classes.LayerCeiling {
		t.Fatalf("operator = %v, origin %v; want false from the ceiling", r.Operator, r.Origin)
	}
	on := ceiling(t, "version: 1\npermissions: {git: {request_push: false, workflows: never}, operator: true}\n")
	d := classes.DiffCeilings(classes.DefaultCeiling(), on)
	if err := classes.AuthorizeCeiling(d, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaDecree, Decree: 7}); !errors.Is(err, classes.ErrCeilingOwnerOnly) {
		t.Fatalf("an Administrator's ceiling decree: %v; want ErrCeilingOwnerOnly", err)
	}
	if err := classes.AuthorizeCeiling(d, classes.Authority{Level: classes.LevelOwner, Via: classes.ViaDecree, Decree: 7}); err != nil {
		t.Fatalf("the Owner's ceiling decree: %v", err)
	}
}

func TestDiffCeilingsTellsRestrictingFromWidening(t *testing.T) {
	t.Parallel()
	base := ceiling(t, docCeiling)
	for name, tc := range map[string]struct {
		edit    func(string) string
		widens  bool
		mention string
	}{
		"same":      {func(s string) string { return s + "# comment\n" }, false, ""},
		"deny more": {func(s string) string { return strings.Replace(s, `"terraform *"]`, `"terraform *", "kubectl *"]`, 1) }, false, ""},
		"deny less": {func(s string) string { return strings.Replace(s, `"docker *", `, "", 1) }, true, "docker *"},
		"deny to ask": {func(s string) string {
			return strings.Replace(strings.Replace(s, `"docker *", `, "", 1), `ask: ["npm publish *"]`, `ask: ["npm publish *", "docker *"]`, 1)
		}, true, "docker *"},
		"ask to deny": {func(s string) string {
			return strings.Replace(strings.Replace(s, `ask: ["npm publish *"]`, `ask: []`, 1), `"terraform *"]`, `"terraform *", "npm publish *"]`, 1)
		}, false, ""},
		"domain added": {func(s string) string { return strings.Replace(s, `"github.com"]`, `"github.com", "example.com"]`, 1) }, true, "example.com"},
		"domains dropped": {func(s string) string {
			return strings.Replace(s, `    domains: ["registry.npmjs.org", "github.com"]`+"\n", "", 1)
		}, true, "network.domains"},
		"write narrowed":   {func(s string) string { return strings.Replace(s, `write: ["**"]`, `write: ["src/**"]`, 1) }, false, ""},
		"cost raised":      {func(s string) string { return strings.Replace(s, "per_table: 50", "per_table: 80", 1) }, true, "cost.usd.per_table"},
		"cost unit gone":   {func(s string) string { return strings.Replace(s, "  hours: {per_table: 20}\n", "", 1) }, true, "cost.hours"},
		"push freed":       {func(s string) string { return strings.Replace(s, "request_push: false", "request_push: true", 1) }, true, "request_push"},
		"workflows to ask": {func(s string) string { return strings.Replace(s, "workflows: never", "workflows: ask", 1) }, true, "workflows"},
		"cli narrowed":     {func(s string) string { return strings.Replace(s, `"issue *", `, "", 1) }, false, ""},
	} {
		d := classes.DiffCeilings(base, ceiling(t, tc.edit(docCeiling)))
		if d.OnlyRestricts() == tc.widens || (tc.mention != "" && !strings.Contains(strings.Join(d.Widens, ";"), tc.mention)) {
			t.Errorf("%s: widens %v, restricts %v; want widening %v citing %q", name, d.Widens, d.Restricts, tc.widens, tc.mention)
		}
	}
}

func TestAuthorizeCeilingFollowsWhoChangesIt(t *testing.T) {
	t.Parallel()
	restrict := classes.CeilingDiff{Restricts: []string{"x"}}
	widen := classes.CeilingDiff{Widens: []string{"x"}}
	for _, tc := range []struct {
		d    classes.CeilingDiff
		by   classes.Authority
		want error
	}{
		{restrict, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}, nil},
		{restrict, classes.Authority{Level: classes.LevelGrandMaster, Via: classes.ViaPanel}, classes.ErrCeilingProposeOnly},
		{restrict, classes.Authority{Level: classes.LevelApprover, Via: classes.ViaDecree, Decree: 1}, classes.ErrCeilingProposeOnly},
		{widen, classes.Authority{Level: classes.LevelOwner, Via: classes.ViaPanel}, classes.ErrCeilingNeedsDecree},
		{widen, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaDecree}, classes.ErrCeilingNeedsDecree},
		{widen, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaDecree, Decree: 3}, nil},
	} {
		if err := classes.AuthorizeCeiling(tc.d, tc.by); !errors.Is(err, tc.want) {
			t.Errorf("%+v by %+v: %v; want %v", tc.d, tc.by, err, tc.want)
		}
	}
}

func TestAHandEditOnlyHoldsOnceApproved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k := newKeeper(t)
	admin := classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}
	// the example of the doc lifts the secret globs of the default ceiling: a decree
	decree := classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaDecree, Decree: 1}
	if err := k.Change(ctx, []byte(docCeiling), decree); err != nil {
		t.Fatal(err)
	}
	approved := ceiling(t, docCeiling).Hash

	// Invalid: the approved version holds, one alert.
	k.File.(*memCeiling).set([]byte("version: 2\n"))
	for range 2 {
		if st, err := k.Check(ctx); err != nil || st.Approved != approved {
			t.Fatalf("check = %+v, %v", st, err)
		}
	}
	if a := k.Alerts.(*alertLog).all(); len(a) != 1 || a[0].Kind != classes.CeilingInvalid {
		t.Fatalf("alerts = %+v; want one invalid", a)
	}

	// Restricting only: pending until an Administrator approves on the panel.
	narrower := strings.Replace(docCeiling, `"terraform *"]`, `"terraform *", "kubectl *"]`, 1)
	k.File.(*memCeiling).set([]byte(narrower))
	st, err := k.Check(ctx)
	if err != nil || st.Pending == nil || st.Alert.Kind != classes.CeilingRestricts {
		t.Fatalf("check = %+v, %v; want a pending restriction", st, err)
	}
	if cur, _ := k.Current(ctx); cur.Hash != approved {
		t.Fatal("the hand edit holds before the approval")
	}
	if err := k.ApprovePending(ctx, "other", admin); !errors.Is(err, classes.ErrCeilingChanged) {
		t.Fatalf("approving another version: %v", err)
	}
	if err := k.ApprovePending(ctx, st.Pending.Hash, classes.Authority{Level: classes.LevelGrandMaster, Via: classes.ViaPanel}); !errors.Is(err, classes.ErrCeilingProposeOnly) {
		t.Fatalf("the Grand Master approving: %v", err)
	}
	if err := k.ApprovePending(ctx, st.Pending.Hash, admin); err != nil {
		t.Fatal(err)
	}
	if cur, _ := k.Current(ctx); cur.Hash != st.Pending.Hash {
		t.Fatal("the approved restriction does not hold")
	}

	// Gone: the approved version holds, alerted.
	k.File.(*memCeiling).set(nil)
	if st, err := k.Check(ctx); err != nil || st.Alert == nil || st.Alert.Kind != classes.CeilingMissing {
		t.Fatalf("check = %+v, %v; want missing", st, err)
	}
}

func TestTheCostCeilingCapsTheLimits(t *testing.T) {
	t.Parallel()
	c := ceiling(t, docCeiling)
	if err := c.CheckLimit(classes.UnitUSD, classes.ScopeTable, 60); !errors.Is(err, classes.ErrAboveCostCeiling) {
		t.Fatalf("a table limit of 60 USD: %v", err)
	}
	for _, ok := range []struct {
		u classes.Unit
		s classes.Scope
		v float64
	}{{classes.UnitUSD, classes.ScopeTable, 50}, {classes.UnitTokens, classes.ScopeDay, 1e9}, {classes.UnitUSD, classes.ScopeWorkspace, 1e6}} {
		if err := c.CheckLimit(ok.u, ok.s, ok.v); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	if l, from := c.EffectiveLimit(classes.UnitHours, classes.ScopeMission, nil); l == nil || *l != 20 || !from {
		t.Fatalf("a mission with no limit in hours: %v from the ceiling %v; want 20", l, from)
	}
	ten := 10.0
	if l, from := c.EffectiveLimit(classes.UnitUSD, classes.ScopeDay, &ten); *l != 10 || from {
		t.Fatalf("a daily limit of 10 USD: %v, %v", *l, from)
	}
}

func newKeeper(t testing.TB) *classes.CeilingKeeper {
	t.Helper()
	return &classes.CeilingKeeper{
		Workspace: "loja", File: &memCeiling{}, Records: &memRecords{}, Alerts: &alertLog{}, Clock: clock.NewFake(now),
	}
}

// memCeiling is teto.yaml in memory.
type memCeiling struct {
	mu   sync.Mutex
	data []byte
}

func (m *memCeiling) set(d []byte) { m.mu.Lock(); m.data = slices.Clone(d); m.mu.Unlock() }

func (m *memCeiling) Read(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, fs.ErrNotExist
	}
	return slices.Clone(m.data), nil
}

func (m *memCeiling) Write(_ context.Context, d []byte) error { m.set(d); return nil }

// memRecords is the approved versions in memory.
type memRecords struct {
	mu sync.Mutex
	m  map[string]classes.CeilingRecord
}

func (r *memRecords) Approved(_ context.Context, ws string) (classes.CeilingRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.m[ws]
	return rec, ok, nil
}

func (r *memRecords) Approve(_ context.Context, ws string, rec classes.CeilingRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]classes.CeilingRecord{}
	}
	r.m[ws] = rec
	return nil
}

// alertLog records the alerts to the Administrators; c, when set, also gets
// each one.
type alertLog struct {
	mu   sync.Mutex
	list []classes.CeilingAlert
	c    chan classes.CeilingAlert
}

func (a *alertLog) CeilingChanged(_ context.Context, al classes.CeilingAlert) error {
	a.mu.Lock()
	a.list = append(a.list, al)
	a.mu.Unlock()
	if a.c != nil {
		a.c <- al
	}
	return nil
}

func (a *alertLog) all() []classes.CeilingAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.list)
}
