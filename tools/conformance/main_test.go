package main

import (
	"slices"
	"strings"
	"testing"
)

func TestBaseEnvOnWindowsKeepsTheAccountVariablesAndTheProcessPath(t *testing.T) {
	t.Parallel()
	vars := map[string]string{"PATH": `C:\Windows\System32;C:\Program Files\Git\cmd`, "SystemRoot": `C:\Windows`, "USERPROFILE": `C:\Users\u`, "SECRET_TOKEN": "x"}
	o := options{cli: "C:/cli/claude.exe", provider: "claude-code", home: `C:\p`}
	env := baseEnvFor("windows", o, func(k string) string { return vars[k] })
	for _, want := range []string{`PATH=C:/cli;C:\Windows\System32;C:\Program Files\Git\cmd`, `SystemRoot=C:\Windows`, `USERPROFILE=C:\Users\u`, `HOME=C:\p`} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q: %v", want, env)
		}
	}
	for _, e := range env {
		if strings.HasPrefix(e, "SECRET_TOKEN") || strings.HasPrefix(e, "LANG=") {
			t.Errorf("env has %q, which is not of a Windows terminal: %v", e, env)
		}
	}
}

func TestBaseEnvOfAntigravityOnWindowsLeavesHomeAndUserProfileToTheEngine(t *testing.T) {
	t.Parallel()
	vars := map[string]string{"PATH": `C:\Windows\System32`, "SystemRoot": `C:\Windows`, "USERPROFILE": `C:\Users\u`}
	o := options{cli: `C:\agy\agy.exe`, provider: "antigravity", home: `C:\p`}
	env := baseEnvFor("windows", o, func(k string) string { return vars[k] })
	if !slices.Contains(env, `SystemRoot=C:\Windows`) {
		t.Errorf("env lacks SystemRoot: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "USERPROFILE=") || strings.HasPrefix(e, "HOME=") {
			t.Errorf("env has %q, which the adapter of Antigravity sets: %v", e, env)
		}
	}
}

func TestBaseEnvOnLinuxIsTheFixedOne(t *testing.T) {
	t.Parallel()
	o := options{cli: "/opt/cli/claude", provider: "claude-code", home: "/h"}
	env := baseEnvFor("linux", o, func(string) string { return "ignored" })
	if !slices.Contains(env, "PATH=/opt/cli:/usr/local/bin:/usr/bin:/bin") || !slices.Contains(env, "HOME=/h") {
		t.Errorf("env = %v", env)
	}
}
