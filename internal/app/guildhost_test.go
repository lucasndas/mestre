package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/panel"
)

// CA-ADM-11
// What the registry refuses for the planned switch reaches the screen as a refusal
// with a stable reason, and a machine that does not exist as not found.
func TestTheRefusalsOfTheRegistryReachTheGuildScreenWithTheirReason(t *testing.T) {
	t.Parallel()
	for err, reason := range map[error]string{
		guild.ErrExcludedMachine: "excluded_machine", guild.ErrOutsideLocalNetwork: "outside_local_network", guild.ErrCitadelProtected: "citadel_protected",
		guild.ErrNotActive: "not_active", guild.ErrNotRevealed: "not_revealed", guild.ErrNoLock: "no_lock", guild.ErrBadTimezone: "bad_timezone", guild.ErrNotPending: "not_pending",
	} {
		var ref *panel.Refusal
		if got := refusalOf(fmt.Errorf("wrapped: %w", err)); !errors.As(got, &ref) || ref.Msg.Code != "guild."+reason {
			t.Errorf("%v -> %v, want reason %s", err, got, reason)
		}
	}
	if got := refusalOf(guild.ErrNoMachine); !errors.Is(got, panel.ErrGuildNotFound) {
		t.Errorf("no machine -> %v", got)
	}
}

func TestTheSheetSummaryDropsTheMarksOfTheMachinesOwnWords(t *testing.T) {
	t.Parallel()
	s := machinesheet.Sheet{OS: "linux", Providers: []machinesheet.Provider{{Provider: "codex", Profiles: []machinesheet.Profile{{Name: "*b*", LoggedIn: true}, {Name: "a"}}}},
		Browser: &machinesheet.Browser{Name: "<img>", Version: "1"}, Operador: machinesheet.Operator{Enabled: true, Reason: "sem_usuario"}}
	got := sheetSummary(s)
	if got.Providers[0] != "codex: b" || got.Browser != "img 1" || got.Operator != "not_ready" || got.OperatorReason != "sem_usuario" {
		t.Errorf("summary = %+v", got)
	}
}

// A planned handover asked in the panel takes the only place: the second call is
// refused while the first is under way, and a Citadel that does not act takes none.
func TestOnlyOnePlannedHandoverRunsAtATime(t *testing.T) {
	t.Parallel()
	run := &citadelRun{}
	reason := func(err error) string {
		var ref *panel.Refusal
		if !errors.As(err, &ref) {
			return fmt.Sprint(err)
		}
		return ref.Msg.Code
	}
	if err := run.claimHandover(true); err != nil {
		t.Fatalf("the first: %v", err)
	}
	if got := reason(run.claimHandover(true)); got != "guild.handover_in_progress" {
		t.Errorf("the second: %q", got)
	}
	run.handing.Store(false) // the handover ended
	if got := reason(run.claimHandover(false)); got != "guild.suspended" {
		t.Errorf("a Citadel that does not act: %q", got)
	}
	if err := run.claimHandover(true); err != nil {
		t.Errorf("a refused claim must free the place: %v", err)
	}
}

type fakeFence struct {
	state     guild.FenceState
	acting    bool
	tgStopped bool
}

func (f fakeFence) Acting() bool            { return f.acting }
func (f fakeFence) State() guild.FenceState { return f.state }
func (f fakeFence) TelegramStopped() bool   { return f.tgStopped }

// CA-TRAVA-07: the row of the Citadel says "GitHub fora" when the lock is down (the Citadel
// still acts), the Telegram stopped by a 409, and suspended when the fence does not act or drains.
func TestTheRowOfTheCitadelFollowsTheStateOfTheFence(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		f                     fakeFence
		draining              bool
		suspended, down, stop bool
	}{
		"confirmed":        {f: fakeFence{state: guild.FenceConfirmed, acting: true}},
		"lock down":        {f: fakeFence{state: guild.FenceLockDown, acting: true}, down: true},
		"telegram stopped": {f: fakeFence{state: guild.FenceLockDown, acting: true, tgStopped: true}, down: true, stop: true},
		"not acting":       {f: fakeFence{state: guild.FenceConfirmed}, suspended: true},
		"draining":         {f: fakeFence{state: guild.FenceConfirmed, acting: true}, draining: true, suspended: true},
	} {
		s, d, st := fenceFlags(c.f, c.draining)
		if s != c.suspended || d != c.down || st != c.stop {
			t.Errorf("%s: suspended %v, github down %v, telegram stopped %v; want %v, %v, %v", name, s, d, st, c.suspended, c.down, c.stop)
		}
	}
}
