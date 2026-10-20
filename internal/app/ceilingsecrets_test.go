package app_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// Part of CA-TETO-07 and CA-TETO-08: the rules the Citadel builds for a
// character of a class without files.deny, under the default ceiling of the
// workspace, cut the reading of the files of secrets, and the origin of the
// globs is the ceiling.
func TestTheEffectiveRulesTheCitadelBuildsCutTheSecretsOfTheDefaultCeiling(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	dev := w.recruit("Dev-T", "Dev Claude")
	r, err := w.cc.Rules(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "mesas", "42-login")
	env := classes.Env{AgentID: dev.ID, Worktree: wt}
	read := func(rel string) classes.Decision {
		return classes.Decide(t.Context(), env, r, providers.Event{Event: providers.EventPreTool, AgentID: dev.ID, Cwd: wt,
			Tool: &providers.Tool{NativeName: "Read", Kind: providers.KindRead, Paths: []string{filepath.Join(wt, filepath.FromSlash(rel))}}})
	}
	for _, secret := range []string{"config/service-account-prod.json", ".env.local", ".env.production", ".env.development", ".env.example"} {
		if d := read(secret); d.Verdict != classes.Deny || d.Layer != classes.LayerCeiling || !strings.Contains(d.Reason, "teto do workspace") {
			t.Errorf("reading %s = %s by %s (%q); want a denial of the ceiling", secret, d.Verdict, d.Layer, d.Reason)
		}
	}
	if d := read("config/settings.json"); d.Verdict != classes.Allow {
		t.Errorf("reading config/settings.json = %s (%q); want allow", d.Verdict, d.Reason)
	}
	for _, glob := range []string{"**/.env*", "**/service-account*.json"} {
		if !slices.Contains(r.Files.Deny, glob) || r.Origin["files.deny:"+glob] != classes.LayerCeiling {
			t.Errorf("files.deny = %q; want %s with the origin ceiling (got %q)", r.Files.Deny, glob, r.Origin["files.deny:"+glob])
		}
	}
}
