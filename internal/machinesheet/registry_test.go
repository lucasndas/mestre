package machinesheet_test

import (
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

type citadelRig struct {
	db       *store.DB
	reg      *machinesheet.Registry
	presence *presence
	names    *names
	mu       sync.Mutex
	changes  []string
}

func newCitadel(t *testing.T) *citadelRig {
	t.Helper()
	r := &citadelRig{db: newGuildDB(t), presence: newPresence(), names: &names{}}
	var err error
	r.reg, err = machinesheet.NewRegistry(machinesheet.RegistryConfig{
		DB: r.db, Presence: r.presence, Names: r.names, Self: func() string { return "citadel" },
		OnChange: func(id string, before, after machinesheet.Sheet) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.changes = append(r.changes, id)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *citadelRig) changed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.changes...)
}

// hello sends the opening of a Tower.
func (r *citadelRig) hello(t *testing.T, id string, o protocol.Object) error {
	t.Helper()
	return r.reg.OnHello(t.Context(), protocol.Peer{Role: protocol.RoleMachine, ID: id}, &protocol.Hello{Sheet: o})
}

// patch delivers a machine.sheet message to the handler.
func (r *citadelRig) patch(t *testing.T, id string, o protocol.Object) error {
	t.Helper()
	_, err := r.reg.Handler().Handle(t.Context(), &protocol.Message{
		Env: protocol.Envelope{Type: protocol.TypeMachineSheet}, Payload: &o, Peer: protocol.Peer{Role: protocol.RoleMachine, ID: id},
	})
	return err
}

// towerSheet is the sheet a Tower built for a fake machine.
func towerSheet(t *testing.T, m *sheetfake.Machine, id, instance string) machinesheet.Sheet {
	t.Helper()
	b, err := machinesheet.NewBuilder(m.Sources(), id, instance)
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Build(t.Context(), machinesheet.Sheet{})
	if err != nil {
		t.Fatal(err)
	}
	s.Rev = 100
	return s
}

func codeOf(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// CA-FMAQ-01: the Citadel side. The Tower sends only the provider that
// changed and the Citadel stores the new sheet.
func TestTheCitadelStoresTheSheetOfTheHelloAndThenThePatches(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "torre-1")
	m := sheetfake.Linux("linux-pc")
	tower := towerSheet(t, m, "m1", "")
	if err := r.hello(t, "m1", tower.Object()); err != nil {
		t.Fatal(err)
	}
	got, err := r.reg.Sheet(t.Context(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MachineID != "m1" || got.Name != "linux-pc" || got.Instance != "torre-1" || got.OS != "linux" || !got.Providers[0].Profiles[0].LoggedIn {
		t.Fatalf("sheet = %+v", got)
	}

	// the login of the profile falls: the patch has only the provider
	fell := tower.Clone()
	fell.Rev = 101
	fell.Providers[0].Profiles[0].LoggedIn = false
	patch, changed := machinesheet.Diff(tower, fell)
	if !changed || !slices.Equal(keys(patch), []string{"providers"}) {
		t.Fatalf("patch = %v", keys(patch))
	}
	if err := r.patch(t, "m1", patch); err != nil {
		t.Fatal(err)
	}
	got, err = r.reg.Sheet(t.Context(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Providers[0].Profiles[0].LoggedIn || got.Rev != 101 {
		t.Errorf("the Citadel kept %+v", got.Providers)
	}
	if got.Resources.MemBytes != tower.Resources.MemBytes || got.OSVersion != "Ubuntu 26.04" {
		t.Errorf("the fields that did not change were lost: %+v", got)
	}
	if want := []string{"m1", "m1"}; !slices.Equal(r.changed(), want) {
		t.Errorf("changes = %v, want %v", r.changed(), want)
	}
}

func TestAnOfflineMachineKeepsItsLastSheetWithOnlineFalse(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	if err := r.hello(t, "m1", towerSheet(t, sheetfake.Linux("linux-pc"), "m1", "").Object()); err != nil {
		t.Fatal(err)
	}
	for _, up := range []bool{true, false} {
		r.presence.set("m1", up)
		s, err := r.reg.Sheet(t.Context(), "m1")
		if err != nil {
			t.Fatal(err)
		}
		if s.Online != up || s.OS != "linux" || s.Resources.CPUCores != 8 {
			t.Errorf("connected %v: online %v, sheet %+v", up, s.Online, s)
		}
	}
	// the machine that holds the Citadel has no connection to itself
	addMachine(t, r.db, "citadel", "citadel-pc", "")
	if s, err := r.reg.Sheet(t.Context(), "citadel"); err != nil || !s.Online {
		t.Errorf("the Citadel is online: %v, %v", s.Online, err)
	}
}

func TestAHelloWithoutTheRequiredFieldsIsInvalidPayloadAndStoresNothing(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	for _, field := range []string{"os", "arch", "providers"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			o := towerSheet(t, sheetfake.Linux("linux-pc"), "m1", "").Object()
			delete(o, field)
			if err := r.hello(t, "m1", o); codeOf(err) != protocol.CodeInvalidPayload {
				t.Errorf("hello without %s = %v, want invalid_payload", field, err)
			}
		})
	}
	if s, err := r.reg.Sheet(t.Context(), "m1"); err != nil || s.OS != "" {
		t.Errorf("a refused hello stored %+v, %v", s, err)
	}
	if len(r.changed()) != 0 {
		t.Errorf("changes = %v", r.changed())
	}
}

func TestAMachineOutsideTheGuildIsForbidden(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	if _, err := r.db.Writer().Exec(`UPDATE machines SET state = 'removed' WHERE machine_id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	o := towerSheet(t, sheetfake.Linux("linux-pc"), "m1", "").Object()
	for _, id := range []string{"m1", "ghost"} {
		if err := r.hello(t, id, o); codeOf(err) != protocol.CodeForbidden {
			t.Errorf("hello of %s = %v, want forbidden", id, err)
		}
		if err := r.patch(t, id, o); codeOf(err) != protocol.CodeForbidden {
			t.Errorf("patch of %s = %v, want forbidden", id, err)
		}
	}
	if _, err := r.reg.Sheet(t.Context(), "m1"); !errors.Is(err, machinesheet.ErrNotActive) {
		t.Errorf("Sheet of a removed machine = %v", err)
	}
}

func TestAPatchOlderThanTheSheetIsAcknowledgedAndDropped(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	tower := towerSheet(t, sheetfake.Linux("linux-pc"), "m1", "")
	if err := r.hello(t, "m1", tower.Object()); err != nil {
		t.Fatal(err)
	}
	// a patch that waited in the outbox while the Tower reconnected, with a
	// revision below the hello that came first
	old := protocol.Object{"rev": json.RawMessage("99"), "transcricao": json.RawMessage("true")}
	if err := r.patch(t, "m1", old); err != nil {
		t.Fatalf("a stale patch is refused: %v", err)
	}
	if s, _ := r.reg.Sheet(t.Context(), "m1"); s.Transcricao || s.Rev != 100 {
		t.Errorf("the stale patch changed the sheet: %+v", s)
	}
	// without a revision it is not a patch of this version
	if err := r.patch(t, "m1", protocol.Object{"gui": json.RawMessage("true")}); codeOf(err) != protocol.CodeInvalidPayload {
		t.Errorf("a patch without a revision = %v", err)
	}
	// a patch that would leave the sheet invalid is refused whole
	bad := protocol.Object{"rev": json.RawMessage("101"), "providers": json.RawMessage("null")}
	if err := r.patch(t, "m1", bad); codeOf(err) != protocol.CodeInvalidPayload {
		t.Errorf("a patch that removes the providers = %v", err)
	}
	if s, _ := r.reg.Sheet(t.Context(), "m1"); s.Rev != 100 || s.Providers == nil {
		t.Errorf("the refused patch changed the sheet: %+v", s)
	}
}

func TestTheHostNameAndTheAddressesOfTheSheetReachTheRegistryOfNames(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	r.names.machine = guild.Machine{ID: "m1", Hostname: "linux-pc", IPs: []net.IP{net.ParseIP("192.168.1.10")}}
	tower := towerSheet(t, sheetfake.Linux("linux-pc"), "m1", "")
	if err := r.hello(t, "m1", tower.Object()); err != nil {
		t.Fatal(err)
	}
	if got := r.names.got(); len(got) != 0 {
		t.Fatalf("the names did not change but were updated: %v", got)
	}
	moved := tower.Clone()
	moved.Rev, moved.IPs = 101, []string{"192.168.7.7"}
	patch, _ := machinesheet.Diff(tower, moved)
	if err := r.patch(t, "m1", patch); err != nil {
		t.Fatal(err)
	}
	if got := r.names.got(); len(got) != 1 || got[0] != "m1 linux-pc [192.168.7.7]" {
		t.Errorf("names updated = %v", got)
	}
}

func TestTheCitadelAppliesItsOwnSheetWithoutAConnection(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "citadel", "citadel-pc", "")
	m := sheetfake.Linux("citadel-pc")
	b, err := machinesheet.NewBuilder(m.Sources(), "citadel", "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: b, Clock: clock.NewFake(day0)})
	if err != nil {
		t.Fatal(err)
	}
	hello, err := a.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reg.Replace(t.Context(), "citadel", hello); err != nil {
		t.Fatal(err)
	}
	a.Attach(r.reg.Local("citadel"))
	m.SetTranscription(true)
	if err := a.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err := r.reg.Sheet(t.Context(), "citadel")
	if err != nil || !s.Transcricao || !s.Online || s.Name != "citadel-pc" {
		t.Errorf("sheet = %+v, %v", s, err)
	}
}

func TestTheCitadelChoosesAmongTheMachinesOnline(t *testing.T) {
	t.Parallel()
	r := newCitadel(t)
	addMachine(t, r.db, "m1", "linux-pc", "")
	addMachine(t, r.db, "m2", "windows-pc", "")
	addMachine(t, r.db, "m3", "no-sheet-yet", "")
	for id, m := range map[string]*sheetfake.Machine{"m1": sheetfake.Linux("linux-pc"), "m2": sheetfake.Windows("windows-pc")} {
		if err := r.hello(t, id, towerSheet(t, m, id, "").Object()); err != nil {
			t.Fatal(err)
		}
	}
	r.presence.set("m1", true)
	r.presence.set("m2", false) // would meet, but is off
	req, err := machinesheet.ParseRequires(map[string]any{"os": "windows"}, machinesheet.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.reg.Choose(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range c.Verdicts {
		got = append(got, v.String())
	}
	want := []string{
		"linux-pc: o sistema é linux e o pedido é windows",
		"windows-pc: offline",
		"no-sheet-yet: offline",
	}
	if !slices.Equal(got, want) || c.Err() == nil {
		t.Errorf("verdicts = %q, want %q", got, want)
	}
	r.presence.set("m2", true)
	if c, _ = r.reg.Choose(t.Context(), req); len(c.Fit()) != 1 || c.Fit()[0].MachineID != "m2" {
		t.Errorf("fit = %v", c.Fit())
	}
}

func TestARegistryNeedsTheDatabaseAndThePresence(t *testing.T) {
	t.Parallel()
	if _, err := machinesheet.NewRegistry(machinesheet.RegistryConfig{}); err == nil {
		t.Error("an empty configuration was accepted")
	}
}
