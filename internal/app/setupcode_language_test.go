package app_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
)

func setupCodeRefusal(t *testing.T, env map[string]string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	code := app.SetupCodeWith(t.Context(), []string{"--dir", t.TempDir()}, &out, &errb, lookup, interfaceOf(env), func() bool { return false })
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
		code, got := setupCodeRefusal(t, map[string]string{"LANG": "C"})
		if code != 3 || got != "error: mestre setup-code precisa de root (Linux) ou de um administrador (Windows)\n" {
			t.Errorf("LANG=C: exit %d, %q", code, got)
		}
	})
	t.Run("LC_ALL beats LANG", func(t *testing.T) {
		t.Parallel()
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

// interfaceOf is what the interface of a Windows says when the case sets the locale variables
// of a Linux: the first of LC_ALL, LC_MESSAGES and LANG that is not empty. Both systems then read
// their own source and each case proves its language on either, whatever the runner speaks.
func interfaceOf(vars map[string]string) func() string {
	return func() string {
		for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if v := strings.TrimSpace(vars[k]); v != "" {
				return v
			}
		}
		return ""
	}
}
