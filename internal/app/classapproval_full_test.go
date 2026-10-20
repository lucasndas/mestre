//go:build full

package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-CLASSES-06: `mestre run` with the chain of approval. The Grand Master
// proposes a class of the workspace through the CLI; the engine puts a request of
// the type classe_workspace in front of the people, and the Grand Master, who
// proposed it, cannot approve it; the class only comes into use with the
// "Aprovar" of an Administrator.
func TestAClassTheGrandMasterProposesOnlyComesIntoUseWithTheApprovalOfAnAdministrator(t *testing.T) {
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

	for _, sql := range []string{
		`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('loja', 'lucas/Loja', 'lucas/loja', 'https://github.com/lucas/Loja.git', 'main', 'active', 'ready', 1, 1)`,
		fmt.Sprintf(`INSERT INTO characters (id, name, name_key, kind, role, workspace, machine_id, sheet, base, state, lifecycle, created_by_kind, created_at)
			VALUES ('gm-1', 'Grão-Mestre', 'grao-mestre', 'character', 'grand_master', 'loja', '%s', 'Grão-Mestre', 'main', 'busy', 'persistent', 'engine', 1)`, founded.MachineID),
		`INSERT INTO users (id, name, level, state, created_at, updated_at) VALUES ('ana', 'Ana', 'administrator', 'active', 1, 1)`,
		`INSERT INTO user_workspaces (user_id, workspace) VALUES ('ana', 'loja')`,
	} {
		w.must(a, map[string]string{"op": "exec", "sql": sql})
	}
	classState := func() string {
		t.Helper()
		rows := w.must(a, map[string]string{"op": "query", "sql": `SELECT count(*) FROM classes WHERE workspace = 'loja' AND name = 'qa'`}).Rows
		return fmt.Sprint(rows[0][0])
	}

	// the Grand Master proposes: a proposal, not a class in use
	pkg, _ := json.Marshal(map[string]any{"app": "novo", "package": map[string]string{
		"CLASS.md":         "---\nname: qa\ndescription: Classe de teste\napp: novo\n---\nTeste o que a mesa pede.\n",
		"permissions.yaml": "version: 1\nfiles:\n  read: [\"**\"]\n",
	}})
	created := w.must(a, map[string]string{"op": "call", "cmd": "class create", "agent": "gm-1", "args": `["qa"]`, "flags": string(pkg)})
	if created.Call == nil || created.Call.Error == nil || !strings.Contains(created.Call.Error.Message, "is a proposal") {
		t.Fatalf("class create by the Grand Master: %+v; want the answer that it is a proposal", created.Call)
	}
	if got := classState(); got != "0" {
		t.Fatalf("the class is in use (%s rows) before anybody approved it", got)
	}
	req := w.must(a, map[string]string{"op": "query", "sql": `SELECT id, state, type, workspace FROM approval_requests WHERE type = 'classe_workspace'`}).Rows
	if len(req) != 1 || req[0][1] != "open" || req[0][3] != "loja" {
		t.Fatalf("the requests of a class of the workspace: %v; want one, open, in loja", req)
	}
	id := fmt.Sprint(req[0][0])

	// the Grand Master cannot approve what it proposed
	denied := w.must(a, map[string]string{"op": "call", "cmd": "approval decide", "agent": "gm-1", "args": `["` + id + `", "approve"]`, "flags": `{"reason":"é boa"}`})
	if denied.Call == nil || denied.Call.Error == nil || denied.Call.Error.Code != "human_required" {
		t.Fatalf("approval decide by the Grand Master: %+v; want human_required", denied.Call)
	}
	if got := classState(); got != "0" {
		t.Fatalf("the class is in use (%s rows) after the Grand Master tried to approve it", got)
	}

	// an Administrator approves: the class comes into use, by the Administrator
	w.must(a, map[string]string{"op": "decide", "request": id, "user": "ana", "level": "administrator"})
	if got := classState(); got != "1" {
		t.Fatalf("the class has %s approved versions after the Administrator approved; want 1", got)
	}
	by := w.must(a, map[string]string{"op": "query", "sql": `SELECT approved_by, approved_level FROM classes WHERE workspace = 'loja' AND name = 'qa'`}).Rows
	if len(by) != 1 || by[0][0] != "ana" || !strings.Contains(fmt.Sprint(by[0][1]), "admin") {
		t.Errorf("approved by %v; want ana, Administrator", by)
	}
}
