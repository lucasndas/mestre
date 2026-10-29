package app_test

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/i18n"
)

func setupCodeRefusal(t *testing.T, env map[string]string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	code := app.SetupCodeWith(t.Context(), []string{"--dir", t.TempDir()}, &out, &errb, lookup, func() bool { return false })
	if out.Len() != 0 {
		t.Fatalf("a refused setup-code printed %q", out.String())
	}
	return code, errb.String()
}

// CA-IDIOMA-07
// mestre setup-code without privilege refuses in the language of the machine:
// LANG=C is pt-BR, and LC_ALL beats LANG. The prefix error: stays.
func TestSetupCodeRefusesInTheLanguageOfTheMachine(t *testing.T) {
	t.Parallel()
	t.Run("LANG=C is pt-BR", func(t *testing.T) {
		t.Parallel()
		onWindowsTheInterfaceDecides(t, "C")
		code, got := setupCodeRefusal(t, map[string]string{"LANG": "C"})
		if code != 3 || got != "error: mestre setup-code precisa de root (Linux) ou de um administrador (Windows)\n" {
			t.Errorf("LANG=C: exit %d, %q", code, got)
		}
	})
	t.Run("LC_ALL beats LANG", func(t *testing.T) {
		t.Parallel()
		onWindowsTheInterfaceDecides(t, "en_US.UTF-8")
		code, got := setupCodeRefusal(t, map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "pt_BR.UTF-8"})
		if code != 3 || got != "error: mestre setup-code needs root (Linux) or an administrator (Windows)\n" {
			t.Errorf("LC_ALL=en_US: exit %d, %q", code, got)
		}
	})
	// an agent is refused with the one text for agents, whatever the machine says
	_, pt := setupCodeRefusal(t, map[string]string{"MESTRE_AGENT_ID": "x", "LANG": "pt_BR.UTF-8"})
	_, en := setupCodeRefusal(t, map[string]string{"MESTRE_AGENT_ID": "x", "LANG": "en_US.UTF-8"})
	if pt != en || !strings.HasPrefix(pt, "error: an agent never") {
		t.Errorf("the refusal of an agent follows the locale: %q / %q", pt, en)
	}
}

// onWindowsTheInterfaceDecides skips a case that needs the machine to say lang
// through its locale variables when the machine is a Windows whose interface says
// another language: there the interface decides and the variables do not count
// (docs/plataforma/idioma-por-canal.md#cli; TestMachineLanguageIsTheInterfaceLanguageOnWindows
// proves it). The other cases run on both systems.
func onWindowsTheInterfaceDecides(t *testing.T, lang string) {
	t.Helper()
	if runtime.GOOS == "windows" && i18n.MachineLanguage() != i18n.Match(lang) {
		t.Skipf("the interface of this Windows says %s, the case needs %s", i18n.MachineLanguage(), i18n.Match(lang))
	}
}
