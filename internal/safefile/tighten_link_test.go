//go:build !windows

package safefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// Part of CA-SZ-WS-09: the tightening never follows a link and never gives a mode: a link in
// regras/ (what an agent puts there to aim the chmod at a secret) leaves its target as it was.
func TestTightenRulesDoesNotFollowALinkToASecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, secret, 0o600)
	regras := filepath.Join(dir, ".mestre", "regras")
	if err := safefile.MkdirAll(regras); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(regras, "politica-de-aprovacao.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := safefile.TightenRules(filepath.Join(dir, ".mestre")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(secret); st.Mode().Perm() != 0o600 {
		t.Errorf("the target of the link has mode %o, want 600", st.Mode().Perm())
	}
}
