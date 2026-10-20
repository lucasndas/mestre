package cli_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit/localrig"
)

// CA-ANEXO-06: `mestre reply --attach /etc/passwd` is refused by the CLI with
// 3 path_not_allowed and no call reaches the engine; the area is the main clone
// and the worktrees of the tables of the workspace of the process, never the
// folder of git or the folders the engine writes for the characters, and a link
// that leaves the area is looked at where it leads.
func TestReplyAttachOutsideTheWorkspaceIsPathNotAllowed(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		const ws = "/data/workspaces/meu-app"
		p.gitRoot, p.cwd = ws+"/mesas/42-login", ws+"/mesas/42-login/src"
		p.files = map[string][]byte{}
		for _, f := range []string{"/etc/passwd", ws + "/mesas/42-login/src/a.png", ws + "/principal/docs/b.pdf", ws + "/mesas/43-outra/c.png", ws + "/espelho.git/HEAD",
			"/data/workspaces/outro/principal/d.png", ws + "/mesas/42-login/.git/config", ws + "/mesas/42-login/.mestre/agents/x", ws + "/mesas/f.png",
			ws + "/mesas/42-login/.mestre/anexos/e.png"} {
			p.files[f] = []byte{1}
		}
		p.links = map[string]string{ws + "/mesas/42-login/src/link": "/etc/passwd"}
		got := capture(r, "reply")
		for _, path := range []string{"/etc/passwd", "../../../../../../etc/passwd", "link", ws + "/espelho.git/HEAD", "/data/workspaces/outro/principal/d.png",
			ws + "/mesas/42-login/.git/config", ws + "/mesas/42-login/.mestre/agents/x", ws + "/mesas/f.png"} {
			res := p.run(t.Context(), "reply", "veja", "--attach", path, "--json")
			if res.code != 3 || !strings.Contains(res.stdout, `"path_not_allowed"`) {
				t.Errorf("--attach %s = %+v", path, res)
			}
		}
		if got().Cmd != "" {
			t.Errorf("a call reached the engine: %+v", got())
		}
		for _, path := range []string{"a.png", ws + "/principal/docs/b.pdf", ws + "/mesas/43-outra/c.png", "../.mestre/anexos/e.png"} {
			if res := p.run(t.Context(), "reply", "veja", "--attach", path); res.code != 0 {
				t.Errorf("--attach %s = %+v", path, res)
			}
		}
		// a process outside any worktree has no area
		p.gitRoot, p.cwd = "/nowhere", "/home/dev"
		if res := p.run(t.Context(), "reply", "veja", "--attach", ws+"/principal/docs/b.pdf", "--json"); res.code != 3 {
			t.Errorf("--attach from outside a worktree = %+v", res)
		}
	})
}
