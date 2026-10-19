package logonsweep_test

import (
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/winsvc/logonsweep"
)

// Part of CA-TERM-05 (what the sweep takes: the matching logic; the real sweep of a
// logon session is in the real-account tests, because the full suite cannot
// make a logon session of its own)
func TestSelectTakesTheProcessesOfTheSessionOnly(t *testing.T) {
	t.Parallel()
	const mine, other = logonsweep.LUID(0x12345), logonsweep.LUID(0x6789a)
	procs := []logonsweep.Proc{
		{PID: 0, LUID: mine, Known: true},    // Idle
		{PID: 4, LUID: mine, Known: true},    // System
		{PID: 100, LUID: mine, Known: true},  // conhost.exe of the ConPTY
		{PID: 101, LUID: other, Known: true}, // another terminal
		{PID: 102, LUID: mine, Known: true},  // created through WMI
		{PID: 103, LUID: mine},               // token not readable: not taken
		{PID: 104, LUID: mine, Known: true},  // the sweeper
		{PID: 105, LUID: 0, Known: true},     // no session
		{PID: 106, LUID: mine, Known: true},  // a descendant
	}
	got := logonsweep.Select(procs, mine, 104)
	if want := []uint32{100, 102, 106}; !slices.Equal(got, want) {
		t.Fatalf("Select = %v, want %v", got, want)
	}
	if got := logonsweep.Select(procs, 0x1); len(got) != 0 {
		t.Fatalf("a session with no process selected %v", got)
	}
}

// The guard: only the batch logon session of a terminal is swept. The logon
// session of a launcher that is not a service (the CI) is the one of the
// runner, which has the whole machine in it.
func TestOnlyABatchLogonSessionMaySweep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		s    logonsweep.Session
		ok   bool
	}{
		{"a terminal", logonsweep.Session{ID: 0x1f2e3, LogonType: logonsweep.LogonTypeBatch}, true},
		{"SYSTEM", logonsweep.Session{ID: logonsweep.SessionSystem, LogonType: logonsweep.LogonTypeBatch}, false},
		{"local service", logonsweep.Session{ID: logonsweep.SessionLocalService, LogonType: logonsweep.LogonTypeBatch}, false},
		{"network service", logonsweep.Session{ID: logonsweep.SessionNetworkService, LogonType: logonsweep.LogonTypeBatch}, false},
		{"anonymous", logonsweep.Session{ID: logonsweep.SessionAnonymous, LogonType: logonsweep.LogonTypeBatch}, false},
		{"no session", logonsweep.Session{LogonType: logonsweep.LogonTypeBatch}, false},
		{"an interactive logon", logonsweep.Session{ID: 0x1f2e3, LogonType: 2}, false},
		{"a service logon", logonsweep.Session{ID: 0x1f2e3, LogonType: 5}, false},
		{"a network logon", logonsweep.Session{ID: 0x1f2e3, LogonType: 3}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := logonsweep.Allowed(tc.s); (err == nil) != tc.ok {
				t.Fatalf("Allowed(%+v) = %v, want ok=%v", tc.s, err, tc.ok)
			}
		})
	}
}
