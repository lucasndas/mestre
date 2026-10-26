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
		guild.ErrNotActive: "not_active", guild.ErrNoLock: "no_lock", guild.ErrBadTimezone: "bad_timezone", guild.ErrNotPending: "not_pending",
	} {
		var ref *panel.Refusal
		if got := refusalOf(fmt.Errorf("wrapped: %w", err)); !errors.As(got, &ref) || ref.Reason != reason {
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
