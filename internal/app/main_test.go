package app_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/app"
)

// An agent never starts the engine: MESTRE_AGENT_ID in the environment, even
// empty, refuses `mestre run` before anything opens
// (docs/arquitetura/componentes.md#mestre).
func TestTheEngineDoesNotStartWithMESTRE_AGENT_IDInTheEnvironmentEvenEmpty(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "agente-1"} {
		var out bytes.Buffer
		lookup := func(k string) (string, bool) {
			if k == "MESTRE_AGENT_ID" {
				return v, true
			}
			return "", false
		}
		if code := app.Main(t.Context(), []string{"--dir", t.TempDir()}, &out, lookup, "test"); code != 3 {
			t.Errorf("MESTRE_AGENT_ID=%q: exit %d, want 3 (%s)", v, code, out.String())
		}
		if !strings.Contains(out.String(), "MESTRE_AGENT_ID") {
			t.Errorf("MESTRE_AGENT_ID=%q: the error does not say why: %q", v, out.String())
		}
	}
}
