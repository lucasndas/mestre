package panel_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// fakeGuild is the engine of the Guild behind the screen: it records what the
// panel asks and refuses what the test says.
type fakeGuild struct {
	mu       sync.Mutex
	machines []panel.GuildMachineRow
	calls    []string
	passErr  error
}

func (f *fakeGuild) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeGuild) done() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeGuild) View(context.Context) (panel.GuildView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return panel.GuildView{Epoch: 3, Timezone: "UTC", Machines: append([]panel.GuildMachineRow(nil), f.machines...)}, nil
}

func (f *fakeGuild) Removal(_ context.Context, id string) (panel.GuildRemoval, error) {
	return panel.GuildRemoval{Machine: id, Agents: []panel.GuildAgent{{ID: "c1", Name: "Dev-1", Workspace: "loja"}}}, nil
}

func (f *fakeGuild) Decide(_ context.Context, _ users.User, id string, accept, _ bool) error {
	f.record("decide " + id)
	return nil
}

func (f *fakeGuild) Remove(_ context.Context, _ users.User, id string) (panel.GuildRemoved, error) {
	f.record("remove " + id)
	return panel.GuildRemoved{Notified: true}, nil
}

func (f *fakeGuild) SetExcluded(_ context.Context, _ users.User, id string, ex bool) error {
	f.record("exclude " + id)
	return nil
}

func (f *fakeGuild) PassCitadel(_ context.Context, _ users.User, id, approval string) error {
	if f.passErr != nil {
		return f.passErr
	}
	f.record("pass " + id + " " + approval[:7])
	return nil
}

func (f *fakeGuild) SetPriority(_ context.Context, id string, n int) error {
	f.record("priority " + id)
	return nil
}

func (f *fakeGuild) SetTimezone(_ context.Context, z string) error {
	f.record("zone " + z)
	return nil
}

func guildRig(t *testing.T) (*rig, *fakeGuild) {
	t.Helper()
	r := newRig(t)
	f := &fakeGuild{machines: []panel.GuildMachineRow{{ID: "c", Name: "casa", Citadel: true, Online: true, Agents: []panel.GuildAgent{}}}}
	r.panel.SetGuild(f)
	return r, f
}

// CA-ADM-08
func TestTheGuildScreenShowsTheSingleMachineBandUntilATowerIsAccepted(t *testing.T) {
	t.Parallel()
	r, f := guildRig(t)
	tok := r.ownerSession()
	v := decodeInto[panel.GuildView](t, r.call("GET", "/api/guild", nil, opts{cookie: tok}))
	if !v.Single || v.Epoch != 3 || len(v.Machines) != 1 {
		t.Fatalf("a Guild with only the Citadel = %+v", v)
	}
	f.mu.Lock()
	f.machines = append(f.machines, panel.GuildMachineRow{ID: "t1", Name: "torre", Online: true})
	f.mu.Unlock()
	if v = decodeInto[panel.GuildView](t, r.call("GET", "/api/guild", nil, opts{cookie: tok})); v.Single {
		t.Error("the band stays after a Tower entered")
	}
}

// CA-ADM-10
func TestAnAdministratorCannotExcludePassTheCitadelOrChangeThePriorityAndTheOwnerNeedsThePIN(t *testing.T) {
	t.Parallel()
	r, f := guildRig(t)
	f.machines = append(f.machines, panel.GuildMachineRow{ID: "t1", Name: "torre", Online: true, Priority: 1})
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/guild"}, {"GET", "/api/guild/machines/t1/removal"},
		{"POST", "/api/guild/machines/t1/exclusion"}, {"POST", "/api/guild/machines/t1/pass-citadel"},
		{"PUT", "/api/guild/machines/t1/priority"}, {"POST", "/api/guild/machines/t1/remove"},
		{"POST", "/api/guild/joins/j1/decide"}, {"PUT", "/api/guild/timezone"},
	} {
		w := r.call(c.method, c.path, map[string]any{}, opts{cookie: adm, pin: "654321"})
		if w.Code != 403 || errCode(w) != panel.CodeForbiddenLevel {
			t.Errorf("%s %s by an Administrator = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	tok := r.ownerSession()
	if w := r.call("POST", "/api/guild/machines/t1/exclusion", panel.GuildExcludeRequest{Excluded: true}, opts{cookie: tok}); errCode(w) != panel.CodePINRequired {
		t.Errorf("without the PIN = %d %s", w.Code, w.Body)
	}
	if len(f.done()) != 0 {
		t.Fatalf("something was done without the right level or the PIN: %v", f.done())
	}
	pinned := opts{cookie: tok, pin: ownerPIN}
	r.wantOK(r.call("POST", "/api/guild/machines/t1/exclusion", panel.GuildExcludeRequest{Excluded: true}, pinned))
	r.wantOK(r.call("PUT", "/api/guild/machines/t1/priority", panel.GuildPriorityRequest{Priority: 5}, pinned))
	r.wantOK(r.call("PUT", "/api/guild/timezone", panel.GuildTimezoneRequest{Timezone: "America/Sao_Paulo"}, pinned))
	if got := strings.Join(f.done(), ","); got != "exclude t1,priority t1,zone America/Sao_Paulo" {
		t.Errorf("done = %s", got)
	}
	log := r.auditText()
	for _, want := range []string{"panel.guild.exclusion", `"before":{"excluded":false}`, "panel.guild.priority", `"before":{"priority":1}`, "panel.guild.timezone"} {
		if !strings.Contains(log, want) {
			t.Errorf("the audit lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, ownerPIN) {
		t.Error("the PIN is in the audit")
	}
}

// CA-ADM-11
func TestPassingTheCitadelToAnExcludedOrOffNetworkMachineIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	r, f := guildRig(t)
	f.machines = append(f.machines, panel.GuildMachineRow{ID: "t1", Name: "torre", Online: true}, panel.GuildMachineRow{ID: "t2", Name: "longe", Online: true})
	tok := r.ownerSession()
	for _, reason := range []string{"excluded_machine", "outside_local_network"} {
		f.passErr = &panel.Refusal{Msg: i18n.M("guild." + reason)}
		w := r.call("POST", "/api/guild/machines/t1/pass-citadel", nil, opts{cookie: tok, pin: ownerPIN})
		e := decodeInto[panel.ErrorBody](t, w).Error
		if w.Code != 409 || string(e.Code) != "guild."+reason {
			t.Errorf("%s: %d %s", reason, w.Code, w.Body)
		}
	}
	if len(f.done()) != 0 || !strings.Contains(r.auditText(), "panel.guild.citadel_pass_refused") {
		t.Errorf("done %v, audit:\n%s", f.done(), r.auditText())
	}
	f.passErr = nil
	r.wantOK(r.call("POST", "/api/guild/machines/t1/pass-citadel", nil, opts{cookie: tok, pin: ownerPIN}))
	if got := f.done(); len(got) != 1 || !strings.HasPrefix(got[0], "pass t1 painel-") {
		t.Errorf("the planned switch did not start: %v", got)
	}
	// a machine the registry does not know is not found, before anything is asked
	if w := r.call("POST", "/api/guild/machines/nope/pass-citadel", nil, opts{cookie: tok, pin: ownerPIN}); w.Code != 404 {
		t.Errorf("unknown machine = %d %s", w.Code, w.Body)
	}
}

func TestRemovingAMachineNeedsTheWarningToBeConfirmedAndListsTheAgents(t *testing.T) {
	t.Parallel()
	r, f := guildRig(t)
	f.machines = append(f.machines, panel.GuildMachineRow{ID: "t1", Name: "torre"})
	tok := r.ownerSession()
	rm := decodeInto[panel.GuildRemoval](t, r.call("GET", "/api/guild/machines/t1/removal", nil, opts{cookie: tok}))
	if len(rm.Agents) != 1 || rm.Agents[0].Name != "Dev-1" {
		t.Fatalf("removal = %+v", rm)
	}
	w := r.call("POST", "/api/guild/machines/t1/remove", panel.GuildRemoveRequest{}, opts{cookie: tok, pin: ownerPIN})
	if e := decodeInto[panel.ErrorBody](t, w).Error; string(e.Code) != "guild.confirm_required" || len(f.done()) != 0 {
		t.Fatalf("remove without confirm = %d %s", w.Code, w.Body)
	}
	r.wantOK(r.call("POST", "/api/guild/machines/t1/remove", panel.GuildRemoveRequest{Confirm: true}, opts{cookie: tok, pin: ownerPIN}))
	if got := f.done(); len(got) != 1 || got[0] != "remove t1" {
		t.Errorf("done = %v", got)
	}
}

// The Owner who follows the Guild gets the snapshot and then the view again when it
// changes; a level below is not let in.
func TestTheFollowersOfTheGuildGetTheViewAgainWhenItChanges(t *testing.T) {
	t.Parallel()
	r, f := guildRig(t)
	srv := r.wsServer()
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	c, _, err := r.dial(srv, adm, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, panel.TypeUISubscribe, "01A", panel.UISubscribe{Topics: []string{panel.GuildTopic}})
	var sub panel.UISubscribed
	_ = json.Unmarshal(read(t, c).Payload, &sub)
	if len(sub.Topics) != 0 || len(sub.Rejected) != 1 {
		t.Fatalf("an Administrator follows the Guild: %+v", sub)
	}
	_ = c.CloseNow()

	c, _, err = r.dial(srv, r.ownerSession(), okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "01B", panel.UISubscribe{Topics: []string{panel.GuildTopic}})
	var ev panel.UIEvent
	_ = json.Unmarshal(read(t, c).Payload, &ev)
	if ev.Topic != panel.GuildTopic || ev.Event != panel.EventSnapshot {
		t.Fatalf("the first event is %+v", ev)
	}
	var v panel.GuildView
	_ = json.Unmarshal(ev.Data, &v)
	if !v.Single || v.Epoch != 3 {
		t.Errorf("the snapshot is %+v", v)
	}
	_ = read(t, c) // subscribed

	f.mu.Lock()
	f.machines = append(f.machines, panel.GuildMachineRow{ID: "t1", Name: "torre", Online: true})
	f.mu.Unlock()
	r.clk.BlockUntil(1)
	r.clk.Advance(3 * time.Second)
	_ = json.Unmarshal(read(t, c).Payload, &ev)
	_ = json.Unmarshal(ev.Data, &v)
	if ev.Event != panel.EventGuild || v.Single || len(v.Machines) != 2 {
		t.Errorf("the event is %+v, view %+v", ev, v)
	}
}
