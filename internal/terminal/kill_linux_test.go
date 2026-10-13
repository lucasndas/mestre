package terminal

import (
	"os"
	"testing"
)

// A member of a session that started before the mestre-pty cannot be of its
// terminal, and one from another boot is never taken.
func TestNotBefore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, since, id string
		want            bool
	}{
		{"later", "b1:100", "b1:101", true},
		{"the same tick", "b1:100", "b1:100", true},
		{"earlier", "b1:100", "b1:99", false},
		{"another boot", "b1:100", "b2:500", false},
		{"no boot in one", "b1:100", "500", false},
		{"not a number", "b1:100", "b1:x", false},
		{"numbers compare as numbers", "b1:99", "b1:100", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := notBefore(tc.since, tc.id); got != tc.want {
				t.Errorf("notBefore(%q, %q) = %v, want %v", tc.since, tc.id, got, tc.want)
			}
		})
	}
}

// procUID reads the real user ID from /proc/<pid>/status, which, unlike the
// owner of /proc/<pid>, does not turn root for a process that is not
// dumpable.
func TestProcUID(t *testing.T) {
	t.Parallel()
	if got := procUID(os.Getpid()); got != os.Getuid() {
		t.Errorf("procUID(self) = %d, want %d", got, os.Getuid())
	}
	if got := procUID(1 << 30); got != -1 {
		t.Errorf("procUID of no process = %d, want -1", got)
	}
}

func TestKillTreeOfNeedsAnIdentityAndAnAccount(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		p    ProcID
		uid  int
	}{
		{"no start identity", ProcID{PID: 4242}, 1000},
		{"init", ProcID{PID: 1, Start: "b:1"}, 1000},
		{"root", ProcID{PID: 4242, Start: "b:1"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := KillTreeOf(t.Context(), tc.p, nil, tc.uid); err == nil {
				t.Error("KillTreeOf went on")
			}
		})
	}
}
