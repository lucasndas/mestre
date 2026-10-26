package providers_test

import (
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
)

// CA-NATIVO-01: Antigravity without internet runs without --sandbox on
// Linux (docs/provedores/antigravity.md#flags); on Windows it keeps it,
// unless the conformance fallback turns it off.
func TestAntigravityOfflineHasTheSandboxOnlyOnWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		os       string
		offline  bool
		fallback bool
		sandbox  bool
		gap      bool
	}{
		{"linux", true, false, false, true},
		{"windows", true, false, true, false},
		{"windows", true, true, false, true},
		{"linux", false, false, false, false},
		{"windows", false, false, false, false},
	}
	for _, tt := range tests {
		p := providers.NativePolicy{AgentID: "a1", OS: tt.os, Worktree: "/srv/w", ProfileHome: "/srv/h", Offline: tt.offline}
		p.Fallbacks.AntigravityNoSandbox = tt.fallback
		nc, err := providers.NewAntigravity(providers.Options{}).(providers.NativeTranslator).Native(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(nc.Args, "--sandbox"); got != tt.sandbox {
			t.Errorf("%+v: args = %q, --sandbox = %v, want %v", tt, nc.Args, got, tt.sandbox)
		}
		if providers.AntigravityNoNativeNetwork(p) != tt.gap {
			t.Errorf("%+v: no native network barrier = %v, want %v", tt, !tt.gap, tt.gap)
		}
	}
}
