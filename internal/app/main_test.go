package app_test

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/i18n"
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

// The errors of the start of `mestre run` are for the person at the terminal: in
// the language of the machine, with the fixed prefix error:.
func TestTheStartErrorsOfTheEngineSpeakTheLanguageOfTheMachine(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"pt_BR.UTF-8": "error: não consigo ler a configuração:", "en_US.UTF-8": "error: cannot read the configuration:"} {
		if runtime.GOOS == "windows" && i18n.MachineLanguage() != i18n.Match(lang) {
			continue // the interface of this Windows decides (onWindowsTheInterfaceDecides)
		}
		var out bytes.Buffer
		lookup := func(k string) (string, bool) {
			if k == "LANG" {
				return lang, true
			}
			return "", false
		}
		// a folder with no configuration: the engine does not start
		if code := app.Main(t.Context(), []string{"--dir", t.TempDir()}, &out, lookup, "test"); code == 0 {
			t.Fatalf("%s: the engine started with a broken configuration", lang)
		}
		if !strings.HasPrefix(out.String(), want) {
			t.Errorf("%s: %q", lang, out.String())
		}
	}
}
