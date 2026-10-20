//go:build full

package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// replWorld is a Citadel that hosts the orchestration and a Tower, with one
// workspace (its mirror), one table that has a worktree on both and a character
// of the table on the Tower.
type replWorld struct {
	r        *rig
	a, b     *inproc
	root     string
	src      string
	mirror   string
	citadel  string // the worktree of the table on the Citadel
	towerDir string // and on the Tower
	orc      *Orchestration
	db       *store.DB
}

const (
	rwWS     = "meu-app"
	rwTable  = "3-login"
	rwBranch = "mestre/3-login"
)

func newReplWorld(t *testing.T) *replWorld {
	t.Helper()
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrt")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	w := &replWorld{r: r, root: root}
	w.a, w.b = r.node("a"), nil
	w.b = r.node("b", w.a)
	w.a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	w.b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	w.a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return w.b.id, true }
	}
	r.found(w.a, clock.Real{})
	r.start(w.a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return w.a.acting() && w.a.e.orc.Load() != nil })
	r.join(w.b, w.a, clock.Real{})
	r.start(w.b, clock.Real{})
	r.waitFor("b to connect", func() bool { return w.b.connected(1) })

	w.src = filepath.Join(root, "src")
	if err := os.MkdirAll(w.src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, w.src, "init", "-q")
	writeFile(t, filepath.Join(w.src, "README.md"), "# Meu App\n")
	git(t, w.src, "add", ".")
	git(t, w.src, "commit", "-q", "-m", "chore: initial commit")
	git(t, w.src, "branch", rwBranch)
	w.mirror = filepath.Join(w.a.paths.Workspaces, rwWS, "espelho.git")
	if err := os.MkdirAll(filepath.Dir(w.mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", "--bare", w.src, w.mirror)
	w.citadel = filepath.Join(w.a.paths.Workspaces, rwWS, "mesas", rwTable)
	git(t, w.mirror, "worktree", "add", "-q", w.citadel, rwBranch)

	w.orc = w.a.e.orc.Load()
	ctx := t.Context()
	w.db = orcDB(t, w.a)
	if _, err := w.db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, rwWS); err != nil {
		t.Fatal(err)
	}
	now := clock.Real{}.Now()
	if err := w.orc.Tables.Store().Insert(ctx, tables.Table{Workspace: rwWS, ID: rwTable, Epic: 3, EpicTitle: "Login", Branch: rwBranch, BaseBranch: "main", BaseCommit: git(t, w.src, "rev-parse", "HEAD"),
		State: tables.StateWorking, OpenedBy: "engine", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	g, err := gitx.New(gitx.Options{Clock: clock.Real{}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, w.citadel)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.orc.Tables.Store().PutWorktree(ctx, tables.Worktree{Workspace: rwWS, Table: rwTable, Machine: w.a.id, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common,
		LastCommit: git(t, w.src, "rev-parse", rwBranch), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	live := characters.Character{ID: "c9", Name: "Dev-9", Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: rwWS, Table: rwTable, Machine: w.b.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral, CreatedAt: now}
	if err := w.orc.Registry.Insert(ctx, live); err != nil {
		t.Fatal(err)
	}
	// the branch crosses the connection and the Tower makes the worktree of the table
	if err := w.orc.Tables.SyncTower(ctx, rwWS, rwTable, w.b.id); err != nil {
		t.Fatalf("the sync of the table to the Tower: %v", err)
	}
	w.towerDir = filepath.Join(w.b.paths.Workspaces, rwWS, "mesas", rwTable)
	if _, err := os.Stat(filepath.Join(w.towerDir, "README.md")); err != nil {
		t.Fatalf("the worktree of the Tower has not the code: %v", err)
	}
	return w
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// keep puts an attachment in the main clone of the Citadel and its row (naming
// the tables it was forwarded to) in the database.
func (w *replWorld) keep(t *testing.T, rel, content, tablesJSON string) {
	t.Helper()
	writeFile(t, filepath.Join(w.a.paths.Workspaces, rwWS, "principal", filepath.FromSlash(rel)), content)
	if _, err := w.db.Writer().ExecContext(t.Context(), `INSERT INTO attachments (id, workspace, user_id, channel, name_original, mime_detected, size, sha256, path, tables, created_at)
		VALUES (?, ?, 'u1', 'telegram', 'print.png', 'image/png', ?, ?, ?, ?, 1)`, rel, rwWS, len(content), fmt.Sprintf("%x", sha256.Sum256([]byte(content))), rel, tablesJSON); err != nil {
		t.Fatal(err)
	}
}

// CA-REPL-19: an attachment of a message for a character of the Tower is copied
// into the worktree of the table of the target, and before the rows of the
// database that cite it. Both ways in: the Citadel that forwards it while the
// Tower is connected (the copy is there when the call returns), and the Tower
// that connects after the row was written (the file comes first, then the row,
// and the Tower copies it into its worktree).
func TestAnAttachmentForACharacterOfTheTowerIsCopiedIntoItsTableBeforeTheRowsThatCiteIt(t *testing.T) {
	t.Parallel()
	w := newReplWorld(t)
	ctx := t.Context()
	root, err := filepath.Abs(w.a.paths.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	copies := &attachmentCopies{db: w.db, tables: tables.Store{DB: w.db}, layout: workspaces.Layout{Root: root}, machine: w.a.id,
		towers: towerAttachments{towerClient: w.a.e.towerClient(), send: w.a.e.sendFile}, log: slog.New(slog.DiscardHandler)}

	// the Citadel forwards the attachment of a message: the Tower has it in the table when the call returns
	const first = ".mestre/anexos/2026/10/a1b2-print.png"
	w.keep(t, first, "PNG", "[]")
	if got, err := (messageAttachments{copies}).Forward(ctx, rwWS, rwTable, []string{first}); err != nil || len(got) != 1 || got[0] != first {
		t.Fatalf("Forward = %v, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(w.towerDir, filepath.FromSlash(first))); err != nil || string(b) != "PNG" {
		t.Fatalf("the worktree of the Tower: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(w.citadel, filepath.FromSlash(first))); err != nil || string(b) != "PNG" {
		t.Fatalf("the worktree of the Citadel: %q, %v", b, err)
	}
	// only the Citadel gives the order, and only for the paths of the attachments
	link := w.b.e.towerLink()
	res, err := link.Call(ctx, protocol.Outgoing{Type: protocol.TypeTableAttach, Payload: protocol.TableAttach{Workspace: rwWS, Table: rwTable, Paths: []string{"../x"}}})
	if err == nil && res.Status == protocol.AckOK {
		t.Error("a Tower took a table.attach of its own")
	}

	// the Tower is away; a row that names its table is written; the Tower connects
	w.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	const second = ".mestre/anexos/2026/10/c3d4-erro.pdf"
	w.keep(t, second, "PDF", `["`+rwTable+`"]`)
	bdb := orcDB(t, w.b)
	var early atomic.Bool
	w.r.waitFor("the attachment to reach the table of the Tower", func() bool {
		var n int
		if err := bdb.Reader().QueryRow(`SELECT count(*) FROM attachments WHERE path = ?`, second).Scan(&n); err == nil && n > 0 &&
			!exists(filepath.Join(w.b.paths.Workspaces, rwWS, "principal", filepath.FromSlash(second))) {
			early.Store(true) // the row before the file
		}
		return exists(filepath.Join(w.towerDir, filepath.FromSlash(second)))
	})
	if early.Load() {
		t.Error("the Tower had the row of the attachment before the file")
	}
	if b, _ := os.ReadFile(filepath.Join(w.towerDir, filepath.FromSlash(second))); string(b) != "PDF" {
		t.Errorf("the copy = %q", b)
	}
}

// The rows of a table cite the mirror of the workspace (PrimaryConfig.CitesMirrors
// of the engine): a row that names a commit the Tower does not have yet comes
// after the mirror that holds it.
func TestTheRowOfATableComesAfterTheMirrorThatHoldsItsCommit(t *testing.T) {
	t.Parallel()
	w := newReplWorld(t)
	ctx := t.Context()
	// a commit in the mirror that nobody told the replication about (no MirrorChanged)
	writeFile(t, filepath.Join(w.src, "novo.md"), "novo\n")
	git(t, w.src, "add", ".")
	git(t, w.src, "commit", "-q", "-m", "feat: second")
	head := git(t, w.src, "rev-parse", "HEAD")
	git(t, w.mirror, "fetch", "-q", w.src, "+HEAD:refs/heads/main")
	replica := filepath.Join(w.b.paths.Workspaces, rwWS, "espelho.git")
	has := func() bool {
		return exec.Command("git", "-C", replica, "cat-file", "-e", head+"^{commit}").Run() == nil
	}
	if has() {
		t.Fatal("the Tower has the commit before anything was sent")
	}
	now := clock.Real{}.Now()
	if err := w.orc.Tables.Store().Insert(ctx, tables.Table{Workspace: rwWS, ID: "4-nova", Epic: 4, EpicTitle: "Nova", Branch: "mestre/4-nova", BaseBranch: "main", BaseCommit: head,
		State: tables.StateWorking, OpenedBy: "engine", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	bdb := orcDB(t, w.b)
	var early atomic.Bool
	w.r.waitFor("the row of the table to reach the Tower", func() bool {
		var n int
		if err := bdb.Reader().QueryRow(`SELECT count(*) FROM tables WHERE id = '4-nova'`).Scan(&n); err != nil || n == 0 {
			return false
		}
		if !has() {
			early.Store(true)
		}
		return true
	})
	if early.Load() {
		t.Error("the Tower had the row of the table before the mirror that holds its commit")
	}
}
