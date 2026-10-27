package panel_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// Part of CA-SZ-WS-09: what the screens write under regras/ (the policy and the extra instructions)
// is not writable by the group of the agents, even when the file it replaces was.
func TestTheFilesTheScreensWriteAreNotWritableByTheGroupOfTheAgents(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the ACL of the folder decides on Windows")
	}
	r := newRules(t)
	tok := r.ownerSession()
	_, adm := r.member("Bruno", "bruno", users.Administrator)

	policy := r.writeRule("politica-de-aprovacao.yaml", "versao: 1\n")
	instr := r.writeRule("instrucoes/grao-mestre.md", "antes")
	for _, f := range []string{policy, instr} {
		if err := os.Chmod(f, 0o664); err != nil {
			t.Fatal(err)
		}
	}
	cur := decodeInto[panel.PolicyFile](t, r.call("GET", "/api/workspaces/loja/policy", nil, opts{cookie: tok}))
	text := strings.Replace(cur.Text, "max_arquivos: 25", "max_arquivos: 30", 1)
	if w := r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: text, Version: cur.Version}, opts{cookie: tok}); w.Code != 200 {
		t.Fatalf("save policy = %d %s", w.Code, w.Body)
	}
	if w := r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "grand_master", Text: "depois"}, opts{cookie: adm}); w.Code != 200 {
		t.Fatalf("save instructions = %d %s", w.Code, w.Body)
	}
	for _, f := range []string{policy, instr} {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o644 {
			t.Errorf("%s has mode %o, want 644", filepath.Base(f), st.Mode().Perm())
		}
	}
}
