package machinesheet_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/sheetfake"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func revOf(t *testing.T, o protocol.Object) int64 {
	t.Helper()
	var rev int64
	if err := json.Unmarshal(o["rev"], &rev); err != nil {
		t.Fatalf("rev: %v", err)
	}
	return rev
}

func TestTheHelloCarriesTheWholeSheetWithoutTheFieldsOfTheGuild(t *testing.T) {
	t.Parallel()
	m := sheetfake.Windows("windows-pc")
	m.SetOperator(machinesheet.Operator{Enabled: true, Ready: false, Reason: machinesheet.ReasonLocked})
	b, err := machinesheet.NewBuilder(m.Sources(), "tower1", "torre-2")
	if err != nil {
		t.Fatal(err)
	}
	a, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: b, Clock: clock.NewFake(day0)})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := a.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"os", "os_version", "arch", "gui", "host", "instance", "providers", "browser", "operador", "resources", "tools", "ips", "rev"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("the hello sheet lacks %q: %v", k, keys(obj))
		}
	}
	for _, k := range []string{"online", "excluded", "name", "machine_id"} {
		if _, ok := obj[k]; ok {
			t.Errorf("the Tower sent %q, which only the Guild sets", k)
		}
	}
	var s machinesheet.Sheet
	raw, _ := json.Marshal(obj)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.OS != "windows" || s.Instance != "torre-2" || s.Resources.MemBytes == 0 || !s.Operador.Enabled || s.Operador.Reason != machinesheet.ReasonLocked {
		t.Errorf("sheet = %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("the hello sheet is not valid: %v", err)
	}
}

// CA-FMAQ-01: the Tower side of the criterion; the Citadel side is in
// registry_test.go and wire_test.go.
func TestWhenTheLoginOfAProfileFallsOnlyTheProviderIsSent(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	hello, err := a.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	m.SetProviders(machinesheet.Provider{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{
		{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: false, Conformant: true, Default: true}}})
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := rec.patches()
	if len(got) != 1 {
		t.Fatalf("%d patches, want 1", len(got))
	}
	if k := keys(got[0]); !slices.Equal(k, []string{"providers"}) {
		t.Errorf("the patch has %v, want only providers", k)
	}
	if revOf(t, got[0]) <= revOf(t, hello) {
		t.Errorf("rev %d does not pass the hello's %d", revOf(t, got[0]), revOf(t, hello))
	}
	var ps []machinesheet.Provider
	if err := json.Unmarshal(got[0]["providers"], &ps); err != nil || ps[0].Profiles[0].LoggedIn {
		t.Errorf("providers = %+v, %v", ps, err)
	}
	if cur := a.Current(); cur.Providers[0].Profiles[0].LoggedIn {
		t.Error("the agent still thinks the Citadel knows a login")
	}
}

func TestNothingIsSentWhenNothingChanged(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(rec.patches()); n != 0 {
		t.Errorf("%d patches for a sheet that did not change", n)
	}
}

func TestTheFieldsAreCheckedEveryFiveMinutesOnTheClock(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clk, m.Sources())
	rec := &recorder{ch: make(chan protocol.Object, 8)}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { _ = a.Run(ctx) })
	defer func() { cancel(); wg.Wait() }()
	clk.BlockUntil(1)

	m.SetIPs("192.168.1.99")
	clk.Advance(4*time.Minute + 59*time.Second)
	if n := len(rec.patches()); n != 0 {
		t.Fatalf("%d patches before the five minutes", n)
	}
	clk.Advance(time.Second)
	if k := keys(rec.next(t)); !slices.Equal(k, []string{"ips"}) {
		t.Errorf("the periodic check sent %v, want ips", k)
	}
}

func TestAPokeChecksAtOnce(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clk, m.Sources())
	rec := &recorder{ch: make(chan protocol.Object, 8)}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { _ = a.Run(ctx) })
	defer func() { cancel(); wg.Wait() }()
	clk.BlockUntil(1)

	// the Operator is enabled, then becomes ready: each poke is a patch
	m.SetOperator(machinesheet.Operator{Enabled: true, Reason: machinesheet.ReasonNoUser})
	a.Poke()
	first := rec.next(t)
	m.SetOperator(machinesheet.Operator{Enabled: true, Ready: true})
	a.Poke()
	second := rec.next(t)
	for i, p := range []protocol.Object{first, second} {
		if k := keys(p); !slices.Equal(k, []string{"operador"}) {
			t.Errorf("patch %d has %v, want operador", i, k)
		}
	}
	if revOf(t, second) <= revOf(t, first) {
		t.Error("the revisions do not grow")
	}
	if string(second["operador"]) != `{"enabled":true,"ready":true}` {
		t.Errorf("operador = %s", second["operador"])
	}
}

func TestTheFreeDiskIsReportedOnlyWhenItMovesALot(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	setDisk := func(gib uint64) {
		r, _ := m.Resources(t.Context())
		r.DiskFreeBytes = gib << 30
		m.SetResources(r)
	}
	setDisk(190) // 5% less than 200
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.patches()); n != 0 {
		t.Fatalf("a 5%% change in the disk sent %d patches", n)
	}
	setDisk(100)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rec.patches(); len(got) != 1 || !slices.Equal(keys(got[0]), []string{"resources"}) {
		t.Fatalf("patches = %v", got)
	}
}

func TestAProbeThatFailsKeepsItsPartAndTheOthersStillChange(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	// the login monitor fails and says "no providers" meanwhile: that must not
	// be reported as every login lost
	m.Fail(sheetfake.PartProviders, errors.New("the monitor is restarting"))
	m.SetProviders()
	m.SetTranscription(true)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := rec.patches()
	if len(got) != 1 || !slices.Equal(keys(got[0]), []string{"transcricao"}) {
		t.Fatalf("patches = %v, want only transcricao", got)
	}
	if !a.Current().Providers[0].Profiles[0].LoggedIn {
		t.Error("the login was forgotten")
	}
}

func TestABrowserThatDisappearsIsSentAsNull(t *testing.T) {
	t.Parallel()
	m := sheetfake.Windows("windows-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetBrowser(nil)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := rec.patches()
	if len(got) != 1 || string(got[0]["browser"]) != "null" || len(keys(got[0])) != 1 {
		t.Fatalf("patches = %v", got)
	}
}

func TestAFailedSendIsTriedAgainOnTheNextCheck(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetTranscription(true)
	rec.fail(errors.New("outbox full"))
	if err := a.Refresh(t.Context()); err == nil {
		t.Fatal("a failed send is not reported")
	}
	rec.fail(nil)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rec.patches(); len(got) != 1 || !slices.Equal(keys(got[0]), []string{"transcricao"}) {
		t.Fatalf("patches = %v", got)
	}
}

func TestAHelloAfterPatchesOutranksThem(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	a := newAgent(t, clock.NewFake(day0), m.Sources())
	rec := &recorder{}
	a.Attach(rec)
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetTranscription(true)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	patch := rec.patches()[0]
	// the connection fell with that patch waiting for its ack; the next
	// hello is newer than it
	hello, err := a.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if revOf(t, hello) <= revOf(t, patch) {
		t.Errorf("hello rev %d does not pass the patch's %d", revOf(t, hello), revOf(t, patch))
	}
}

func TestAnAgentNeedsABuilderAndAClock(t *testing.T) {
	t.Parallel()
	if _, err := machinesheet.NewAgent(machinesheet.AgentConfig{}); err == nil {
		t.Error("an empty configuration was accepted")
	}
	if _, err := machinesheet.NewBuilder(machinesheet.Sources{}, "m", ""); !errors.Is(err, machinesheet.ErrNoSystem) {
		t.Errorf("NewBuilder without a system probe = %v", err)
	}
}
