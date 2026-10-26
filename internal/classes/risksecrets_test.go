package classes_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// CA-RISCO-17
func TestTheDefaultSecretPathsAreTheEleventhOfTheDefaultPolicy(t *testing.T) {
	t.Parallel()
	env, r := riskEnv(nil), riskRules()
	// One path for each of the eleven globs of permissoes_e_segredos in the
	// default content of the policy file, at the root and below it.
	for _, p := range []string{
		".env", "config/.env.local", "secrets/db.txt", "app/permissions.yaml", "certs/a.pem", "keys/a.key",
		"id_rsa", "home/.ssh/id_rsa.pub", "id_ed25519", "keys/id_ed25519.pub", "id_ecdsa", "keys/id_ecdsa",
		".npmrc", "web/.npmrc", "credentials.json", "gcp/credentials-prod.json", "service-account.json", "infra/service-account-ci.json",
	} {
		for _, reader := range []string{"cat ", "head -n 1 ", "grep -n token "} {
			if got := shellRisk(env, r, reader+p); got.Risk != classes.RiskHigh {
				t.Errorf("%s%s = %s (%s); want alto, a secret", reader, p, got.Risk, got.Rule)
			}
		}
	}
	// The globs are not wider than they say.
	for _, p := range []string{"README.md", "credentials.go", "src/credentials.ts", "service.json", "service-accounts.go", "docs/idea.txt", "npmrc.md"} {
		if got := shellRisk(env, r, "cat "+p); got.Risk != classes.RiskLow {
			t.Errorf("cat %s = %s (%s); want baixo", p, got.Risk, got.Rule)
		}
	}
	if len(classes.DefaultSecretPaths) != 11 {
		t.Errorf("DefaultSecretPaths has %d globs; the default policy file has 11", len(classes.DefaultSecretPaths))
	}
}
