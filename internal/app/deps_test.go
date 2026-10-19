package app

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/pkg/clock"
)

// New gives the engine the real mDNS browser and the real packet network
// unless the configuration turns discovery off (no_discovery), and keeps the
// ones a test passes.
func TestTheEngineGetsTheSystemBrowserUnlessDiscoveryIsOff(t *testing.T) {
	t.Parallel()
	cfg := Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443}
	paths := DirPaths(t.TempDir())

	on, err := New(cfg, paths, Deps{Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := on.deps.Browser.(discovery.SystemBrowser); !ok {
		t.Errorf("with discovery on, the browser is %T, want discovery.SystemBrowser", on.deps.Browser)
	}
	if on.deps.Packets == nil {
		t.Error("with discovery on, there is no packet network")
	}

	cfg.NoDiscovery = true
	off, err := New(cfg, paths, Deps{Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	if off.deps.Browser != nil || off.deps.Packets != nil {
		t.Errorf("with no_discovery, the engine still has a browser (%T) or packet network (%T)", off.deps.Browser, off.deps.Packets)
	}
}

// The configuration refuses the name of an instance whose agents' account
// would not fit a Windows local account (docs/arquitetura/componentes.md#instâncias).
func TestTheConfigurationRefusesAnInstanceWhoseAccountDoesNotFit(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"principal", "a", "seg-1"} {
		if err := (Config{Instance: name, Name: "a", EnginePort: 7443}).check(); err != nil {
			t.Errorf("instance %q: %v", name, err)
		}
	}
	for _, name := range []string{"abcdefg", "X", strings.Repeat("a", 32)} {
		if err := (Config{Instance: name, Name: "a", EnginePort: 7443}).check(); err == nil {
			t.Errorf("instance %q accepted", name)
		}
	}
}
