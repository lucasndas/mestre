package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// agentOutput is what the commands of an agent print for a few calls (a refused
// one, an unknown one, the help of all and of one command) and what the session
// zero writes as skills, in the locale the process has.
func agentOutput(t *testing.T) string {
	t.Helper()
	// the engine of the test refuses `check` for the agent (a real call through the local channel)
	rig := localrig.New(clock.Real{}, localrig.Options{})
	rig.Start(t)
	rig.Character("tA", "agent-1")
	rig.Process("agent-proc", "tA")
	rig.Forwarder.Handle("check", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
		return localchan.Fail(localchan.CodeDenied, "the engine refuses this check", "ask your superior")
	}))
	// the process of the agent has MESTRE_AGENT_ID and the locale the process of the test has
	getenv := func(k string) string {
		if k == localchan.EnvAgentID {
			return "agent-1"
		}
		return os.Getenv(k)
	}
	var b strings.Builder
	for _, argv := range [][]string{{"check", "Dev-1"}, {"check", "Dev-1", "--json"}, {"bogus"}, {"bogus", "--json"}, {"help"}, {"help", "check"}, {"check", "--help"}} {
		var out, errb bytes.Buffer
		env := cli.Env{Getenv: getenv, Stdout: &out, Stderr: &errb, Clock: clock.Real{}, Cwd: "/work", Version: "test",
			Dial: func(string) localchan.Dialer { return rig.Dialer("agent-proc") }}
		code := cli.Run(context.Background(), env, argv)
		fmt.Fprintf(&b, "== %v exit %d\n-- stdout\n%s-- stderr\n%s", argv, code, out.String(), errb.String())
	}
	// the commands of a person refuse an agent with the one text, whatever the locale
	lookup := func(k string) (string, bool) {
		if k == localchan.EnvAgentID {
			return "agent-1", true
		}
		return os.LookupEnv(k)
	}
	var runErr, codeOut, codeErr bytes.Buffer
	fmt.Fprintf(&b, "== run exit %d\n%s", app.Main(context.Background(), []string{"--dir", os.DevNull}, &runErr, lookup, nil, "test"), runErr.String())
	fmt.Fprintf(&b, "== setup-code exit %d\n%s%s", app.SetupCodeMain(context.Background(), nil, &codeOut, &codeErr, lookup), codeOut.String(), codeErr.String())
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
	// the reference is English (the sentences of the base, of the help and of the skills), and the
	// refused call really went to the engine and was refused for the agent
	for _, want := range []string{"the engine refuses this check", "exit 3", "an agent never starts the engine", "an agent never makes the code of setup", "unknown command", "mestre help"} {
		if !strings.Contains(base, want) {
			t.Errorf("the reference output lacks %q", want)
		}
	}
	for _, pt := range []string{"não", "ção", "comando", "Uso:", "ajuda"} {
		if strings.Contains(base, pt) {
			t.Errorf("the reference output has %q, a word of pt-BR", pt)
		}
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
