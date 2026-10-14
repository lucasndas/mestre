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
