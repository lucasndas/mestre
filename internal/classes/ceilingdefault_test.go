package classes_test

import (
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// defaultCeilingRules are the effective rules of a class that reads everything
// and has no files.deny, under the default ceiling the session zero writes.
func defaultCeilingRules(t *testing.T) *classes.Rules {
	t.Helper()
	class := parse(t, "version: 1\nfiles: {read: [\"**\"]}\n")
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: class, Ceiling: classes.DefaultCeiling().Maximum(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// CA-TETO-07
func TestTheDefaultCeilingDeniesTheReadingOfTheFilesOfSecrets(t *testing.T) {
	t.Parallel()
	r := defaultCeilingRules(t)
	m := newMachine()
	for _, secret := range []string{"config/service-account-prod.json", ".env.local"} {
		want(t, decide(m.env(), r, m.file(providers.KindRead, m.in(secret))), classes.Deny, classes.LayerCeiling, "teto da campanha")
	}
	want(t, decide(m.env(), r, m.file(providers.KindRead, m.in("config/settings.json"))), classes.Allow, "")
	for _, glob := range []string{"**/service-account*.json", "**/.env*"} {
		if !slices.Contains(r.Files.Deny, glob) || r.Origin["files.deny:"+glob] != classes.LayerCeiling {
			t.Errorf("files.deny = %q, origin %q; want %s from the ceiling", r.Files.Deny, r.Origin["files.deny:"+glob], glob)
		}
	}
}

// CA-TETO-08
func TestTheDefaultCeilingDenyOfDotEnvTakesEveryVariantEvenTheExample(t *testing.T) {
	t.Parallel()
	r := defaultCeilingRules(t)
	m := newMachine()
	for _, secret := range []string{".env.production", ".env.development", ".env.example"} {
		want(t, decide(m.env(), r, m.file(providers.KindRead, m.in(secret))), classes.Deny, classes.LayerCeiling, "teto da campanha")
	}
	want(t, decide(m.env(), r, m.file(providers.KindRead, m.in("config/settings.json"))), classes.Allow, "")
	if !slices.Contains(r.Files.Deny, "**/.env*") || r.Origin["files.deny:**/.env*"] != classes.LayerCeiling {
		t.Errorf("files.deny = %q, origin %q; want **/.env* from the ceiling", r.Files.Deny, r.Origin["files.deny:**/.env*"])
	}
}
