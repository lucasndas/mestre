package classes_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// CA-NATIVO-07
func TestCheckSaysWhichProvidersRunWithoutTheNativeNetworkBarrier(t *testing.T) {
	t.Parallel()
	const offline = "version: 1\nnetwork: {internet: false}\n"
	tests := []struct {
		os    string
		perms string
		gap   []providers.ID
	}{
		{"linux", offline, []providers.ID{providers.Codex, providers.Antigravity}},
		{"windows", offline, nil},
		{"linux", "version: 1\n", nil},
	}
	for _, tt := range tests {
		f := newFixture(t)
		f.lib.OS = tt.os
		rep, err := f.lib.CheckFiles(context.Background(), pkg(t, "qa", "novo", tt.perms, nil),
			classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"})
		if err != nil {
			t.Fatal(err)
		}
		var got []providers.ID
		for _, p := range rep.Providers {
			if slices.ContainsFunc(p.DispatcherOnly, func(l string) bool { return strings.HasPrefix(l, "internet: sem reforço nativo de rede") }) {
				got = append(got, p.Provider)
			}
		}
		if !slices.Equal(got, tt.gap) {
			t.Errorf("%s, %q: providers without the native network barrier = %v, want %v", tt.os, tt.perms, got, tt.gap)
		}
	}
}

// CA-NATIVO-09
//
// C7 of the M3 audit: a Codex class without write runs in workspace-write
// (read-only would cut the CLI from the local channel), so the check says it
// has no native write barrier, in Codex only and on both systems.
func TestCheckSaysCodexRunsWithoutTheNativeWriteBarrier(t *testing.T) {
	t.Parallel()
	const noWrite = "version: 1\nfiles: {write: []}\n"
	for _, tt := range []struct {
		os, perms string
		gap       []providers.ID
	}{
		{"linux", noWrite, []providers.ID{providers.Codex}},
		{"windows", noWrite, []providers.ID{providers.Codex}},
		{"linux", "version: 1\n", nil},
	} {
		f := newFixture(t)
		f.lib.OS = tt.os
		rep, err := f.lib.CheckFiles(context.Background(), pkg(t, "qa", "novo", tt.perms, nil),
			classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"})
		if err != nil {
			t.Fatal(err)
		}
		var got []providers.ID
		for _, p := range rep.Providers {
			if slices.ContainsFunc(p.DispatcherOnly, func(l string) bool { return strings.HasPrefix(l, "escrita: sem barreira nativa de escrita") }) {
				got = append(got, p.Provider)
			}
		}
		if !slices.Equal(got, tt.gap) {
			t.Errorf("%s, %q: providers without the native write barrier = %v, want %v", tt.os, tt.perms, got, tt.gap)
		}
	}
}

// CA-NATIVO-13
//
// `class check` translates the controls with the plans B of the machine, as
// a character of it starts: on Windows, with the plans B that keep the
// provider network on, Codex and Antigravity run a class without internet
// with no native network barrier, and the check says so.
func TestCheckUsesThePlansBOfTheMachine(t *testing.T) {
	t.Parallel()
	const offline = "version: 1\nnetwork: {internet: false}\n"
	for _, tt := range []struct {
		name string
		fb   providers.NativeFallbacks
		gap  []providers.ID
	}{
		{"default", providers.NativeFallbacks{}, nil},
		{"codex offline network", providers.NativeFallbacks{CodexOfflineNetwork: true}, []providers.ID{providers.Codex}},
		{"antigravity no sandbox", providers.NativeFallbacks{AntigravityNoSandbox: true}, []providers.ID{providers.Antigravity}},
	} {
		f := newFixture(t)
		f.lib.OS, f.lib.Fallbacks = "windows", tt.fb
		rep, err := f.lib.CheckFiles(context.Background(), pkg(t, "qa", "novo", offline, nil),
			classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"})
		if err != nil {
			t.Fatal(err)
		}
		var got []providers.ID
		for _, p := range rep.Providers {
			if slices.ContainsFunc(p.DispatcherOnly, func(l string) bool { return strings.HasPrefix(l, "internet: sem reforço nativo de rede") }) {
				got = append(got, p.Provider)
			}
		}
		if !slices.Equal(got, tt.gap) {
			t.Errorf("%s: providers without the native network barrier = %v, want %v", tt.name, got, tt.gap)
		}
	}
}
