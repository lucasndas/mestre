//go:build full

package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-REPL-18: the engine that hosts the orchestration sends the mirror of each
// workspace to the Towers. A Tower that connects keeps a replica of the mirror
// under the names of the replication (refs/mestre/espelho/), never in its own
// branches, and the commit is the one of the mirror of the Citadel.
func TestTheEngineSendsTheMirrorOfEachWorkspaceToTheTowers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	a.host = true
	b := r.node("b", a)

	remote := filepath.Join(r.root, "remote")
	writeFile(t, filepath.Join(remote, "README.md"), "# Meu App\n")
	testkit.ProjectGit(t, remote, "init", "-q", "-b", "main")
	testkit.ProjectGit(t, remote, "-c", "user.name=t", "-c", "user.email=t@t", "add", ".")
	testkit.ProjectGit(t, remote, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	head := strings.TrimSpace(testkit.ProjectGit(t, remote, "rev-parse", "HEAD"))
	mirror := filepath.Join(a.paths.Workspaces, "meu-app", "espelho.git")
	writeFile(t, filepath.Join(a.paths.Workspaces, "meu-app", ".keep"), "")
	testkit.ProjectGit(t, r.root, "clone", "-q", "--mirror", remote, mirror)

	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	err := a.e.withGuildDB(func(db *store.DB) error {
		_, err := db.Writer().ExecContext(context.Background(), `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('meu-app', 'lucas/Meu_App', 'lucas/meu_app', ?, 'main', 'active', 'ready', 1, 1)`, remote)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// a workspace whose mirror is gone does not keep the Tower from the others
	err = a.e.withGuildDB(func(db *store.DB) error {
		_, err := db.Writer().ExecContext(context.Background(), `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('sem-espelho', 'lucas/Sem', 'lucas/sem', ?, 'main', 'active', 'ready', 1, 1)`, remote)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	replica := filepath.Join(b.paths.Workspaces, "meu-app", "espelho.git")
	r.waitFor("the mirror to reach the Tower", func() bool {
		out, err := exec.Command("git", "-C", replica, "rev-parse", "refs/mestre/espelho/heads/main").Output()
		return err == nil && strings.TrimSpace(string(out)) == head
	})

	// a ref that moves in the mirror of the Citadel reaches the Tower without a new connection
	writeFile(t, filepath.Join(remote, "novo.md"), "novo\n")
	testkit.ProjectGit(t, remote, "-c", "user.name=t", "-c", "user.email=t@t", "add", ".")
	testkit.ProjectGit(t, remote, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "second")
	head = strings.TrimSpace(testkit.ProjectGit(t, remote, "rev-parse", "HEAD"))
	testkit.ProjectGit(t, mirror, "fetch", "-q", "origin", "+refs/heads/*:refs/heads/*")
	a.e.mu.Lock()
	primary := a.e.cit.primary
	a.e.mu.Unlock()
	primary.MirrorChanged("meu-app")
	r.waitFor("the moved ref to reach the Tower", func() bool {
		out, err := exec.Command("git", "-C", replica, "rev-parse", "refs/mestre/espelho/heads/main").Output()
		return err == nil && strings.TrimSpace(string(out)) == head
	})
}
