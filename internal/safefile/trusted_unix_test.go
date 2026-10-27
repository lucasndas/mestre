//go:build !windows

package safefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

func mustChmod(t *testing.T, p string, m os.FileMode) {
	t.Helper()
	if err := os.Chmod(p, m); err != nil {
		t.Fatal(err)
	}
}

// Part of CA-SZ-WS-09: the engine reads a file of regras/ only when it is made by its own
// account (or root's) and neither the file nor a folder on the way down from .mestre/ can be
// written by the group or the others: the agents write the main clone, and a file they put
// in place of the policy is not the policy. (The owner check needs another account, so the
// CI of the real accounts proves it; here the mode is.)
func TestReadTrustedFailsClosedOnWhatTheGroupCanWrite(t *testing.T) {
	t.Parallel()
	mestre := filepath.Join(t.TempDir(), ".mestre")
	regras := filepath.Join(mestre, "regras")
	file := filepath.Join(regras, "politica-de-aprovacao.yaml")
	if err := safefile.MkdirAll(regras); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Write(file, []byte("versao: 1\n"), safefile.Options{Mode: safefile.RulesMode}); err != nil {
		t.Fatal(err)
	}
	if b, _, err := safefile.ReadTrusted(file, 0); err != nil || string(b) != "versao: 1\n" {
		t.Fatalf("a file of the engine = %q, %v", b, err)
	}

	mustChmod(t, file, 0o664)
	if _, _, err := safefile.ReadTrusted(file, 0); !errors.Is(err, safefile.ErrUntrusted) || !safefile.Refused(err) {
		t.Errorf("a file the group writes = %v, want ErrUntrusted", err)
	}
	mustChmod(t, file, 0o644)

	mustChmod(t, regras, 0o775)
	if _, _, err := safefile.ReadTrusted(file, 0); !errors.Is(err, safefile.ErrUntrusted) {
		t.Errorf("a folder the group writes (regras) = %v, want ErrUntrusted", err)
	}
	mustChmod(t, regras, 0o755)
	mustChmod(t, mestre, 0o777)
	if _, _, err := safefile.ReadTrusted(file, 0); !errors.Is(err, safefile.ErrUntrusted) {
		t.Errorf("a folder the others write (.mestre) = %v, want ErrUntrusted", err)
	}

	// the advance of the clone and an earlier version leave this; the tightening fixes it all
	mustChmod(t, file, 0o666)
	mustChmod(t, regras, 0o775)
	if err := safefile.TightenRules(mestre); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{mestre: 0o755, regras: 0o755, file: 0o644} {
		if st, _ := os.Stat(p); st.Mode().Perm() != want {
			t.Errorf("%s has mode %o, want %o", filepath.Base(p), st.Mode().Perm(), want)
		}
	}
	if _, _, err := safefile.ReadTrusted(file, 0); err != nil {
		t.Errorf("after the tightening: %v", err)
	}
}
