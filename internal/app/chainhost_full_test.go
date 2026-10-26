//go:build full

package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-CIDADELA-18: `mestre run` builds the chain of approval. The process of the
// Citadel opens a request when the rules of a character say ask, through the
// approvals the hook dispatcher of the machine uses, and the request is kept in
// the Guild with the requester, its workspace and the level that decides it;
// the request of a character the registry does not know is refused.
func TestMestreRunOpensTheApprovalRequestOfACharacter(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 1)
	a := w.add("a", ports[0], nil)
	founded, rk, err := app.Found(t.Context(), a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)

	w.must(a, map[string]string{"op": "exec", "sql": `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/Loja', 'lucas/loja', 'https://github.com/lucas/Loja.git', 'main', 'active', 'ready', 1, 1)`})
	w.must(a, map[string]string{"op": "exec", "sql": fmt.Sprintf(`INSERT INTO characters (id, name, name_key, kind, role, workspace, machine_id, sheet, base, state, lifecycle, created_by_kind, created_at)
		VALUES ('gm-1', 'Grão-Mestre', 'grao-mestre', 'character', 'grand_master', 'loja', '%s', 'Grão-Mestre', 'main', 'busy', 'persistent', 'engine', 1)`, founded.MachineID)})

	got := w.must(a, map[string]string{"op": "approval", "agent": "gm-1", "command": "npm install left-pad"})
	if got.Request <= 0 {
		t.Fatalf("no request was opened: %+v", got)
	}
	rows := w.must(a, map[string]string{"op": "query", "sql": fmt.Sprintf(`SELECT workspace, type, state, requester FROM approval_requests WHERE id = %d`, got.Request)}).Rows
	if len(rows) != 1 || rows[0][0] != "loja" || rows[0][1] != "permissao_de_classe" || rows[0][2] != "open" || rows[0][3] != "gm-1" {
		t.Fatalf("the request kept: %v", rows)
	}

	// a character nobody knows has no request
	if _, err := w.ask(a, map[string]string{"op": "approval", "agent": "ghost", "command": "ls"}); err == nil {
		t.Fatal("a request was opened for a character that does not exist")
	}
}
