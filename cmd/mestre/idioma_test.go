package main

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// agentOutput is what the commands of an agent print for a few calls (a refused
// one, an unknown one, the help of all and of one command) and what the session
// zero writes as skills, in the locale the process has.
func agentOutput(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, argv := range [][]string{{"check", "Dev-1"}, {"bogus"}, {"bogus", "--json"}, {"help"}, {"help", "check"}, {"check", "--help"}} {
		var out, errb bytes.Buffer
		env := cli.Env{Getenv: func(string) string { return "" }, Stdout: &out, Stderr: &errb, Clock: clock.Real{}, Cwd: "/work", Version: "test"}
		code := cli.Run(context.Background(), env, argv)
		fmt.Fprintf(&b, "== %v exit %d\n-- stdout\n%s-- stderr\n%s", argv, code, out.String(), errb.String())
	}
	shared, levels := sessionzero.GenerateSkills(cli.Default())
	fmt.Fprintf(&b, "== skill %s\n%s\n", shared.Name, shared.Body)
	keys := make([]string, 0, len(levels))
	for k := range levels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "== skill %s\n%s\n", k, levels[k].Body)
	}
	return b.String()
}

// CA-IDIOMA-06
// The commands of an agent, the help and the skills the session zero generates
// do not read the locale: with LANG=pt_BR.UTF-8 they come out byte for byte as
// with LANG=C, in English.
func TestTheOutputForAgentsIgnoresTheLocale(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "C")
	base := agentOutput(t)
	if !strings.Contains(base, "== skill ") || !strings.Contains(base, "help") {
		t.Fatalf("the reference output is empty: %q", base)
	}
	for name, vars := range map[string]map[string]string{
		"pt_BR":          {"LANG": "pt_BR.UTF-8"},
		"LC_ALL pt_BR":   {"LC_ALL": "pt_BR.UTF-8", "LANG": "en_US.UTF-8"},
		"en_US":          {"LANG": "en_US.UTF-8"},
		"LC_MESSAGES pt": {"LC_MESSAGES": "pt_BR.UTF-8"},
	} {
		for k, v := range vars {
			t.Setenv(k, v)
		}
		if got := agentOutput(t); got != base {
			t.Errorf("%s: the output for agents changed with the locale", name)
		}
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", "C")
	}
}
