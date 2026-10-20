//go:build full

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// git runs the git of the system in dir, for the repositories of the test.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Teste", "-c", "user.email=teste@example.test", "-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// CA-CIDADELA-17: the Citadel reaches the half of the tables of a Tower over
// the connection: it sends the branch of a table, which the Tower turns into a
// worktree with the session zero of the table; asks where it is, how it is and
// removes it; the Tower sends its commits back after a done (and again after
// the connection is back); the signs of life of a long command cross the
// cli.call; and with the Tower away the answer is the one of a machine that
// cannot be reached.
func TestTheCitadelReachesTheTablesOfATower(t *testing.T) {
	t.Parallel()
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
	a := r.node("a")
	b := r.node("b", a)
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	var mu sync.Mutex
	var partials []string
	a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })
	// the signs of life of the long command are the test's: it replaces the real ask
	a.e.Commands().Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
		c.Partial(map[string]string{"status": "queued"})
		c.Partial(map[string]string{"status": "delivered"})
		return localchan.Response{OK: true, Data: json.RawMessage(`{"status":"answered"}`)}
	}))
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	// the workspace of the Citadel: a mirror with the branch of the table, the
	// worktree of the Citadel and the rows that say so
	const ws, table, branch = "meu-app", "3-login", "mestre/3-login"
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, src, "init", "-q")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Meu App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "chore: initial commit")
	git(t, src, "branch", branch)
	mirror := filepath.Join(a.paths.Workspaces, ws, "espelho.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", "--bare", src, mirror)
	citadelTree := filepath.Join(a.paths.Workspaces, ws, "mesas", table)
	git(t, mirror, "worktree", "add", "-q", citadelTree, branch)

	orc := a.e.orc.Load()
	ctx := t.Context()
	db := orcDB(t, a)
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, ws); err != nil {
		t.Fatal(err)
	}
	now := clock.Real{}.Now()
	if err := orc.Tables.Store().Insert(ctx, tables.Table{Workspace: ws, ID: table, Epic: 3, EpicTitle: "Login", Branch: branch, BaseBranch: "main", BaseCommit: git(t, src, "rev-parse", "HEAD"),
		State: tables.StateWorking, OpenedBy: "engine", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	g, err := gitx.New(gitx.Options{Clock: clock.Real{}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, citadelTree)
	if err != nil {
		t.Fatal(err)
	}
	if err := orc.Tables.Store().PutWorktree(ctx, tables.Worktree{Workspace: ws, Table: table, Machine: a.id, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common, LastCommit: git(t, src, "rev-parse", branch), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	// a character of the table lives on the Tower: its commits are welcome
	live := characters.Character{ID: "c9", Name: "Dev-9", Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: ws, Table: table, Machine: b.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral, CreatedAt: now}
	if err := orc.Registry.Insert(ctx, live); err != nil {
		t.Fatal(err)
	}
	peers := towerTables{a.e.towerClient()}
	// the branch crosses the connection and the Tower makes the worktree of the table
	if err := orc.Tables.SyncTower(ctx, ws, table, b.id); err != nil {
		t.Fatalf("the sync of the table to the Tower: %v", err)
	}
	dir, err := peers.WorktreeDir(ctx, b.id, ws, table)
	if want := filepath.Join(b.paths.Workspaces, ws, "mesas", table); err != nil || dir != want {
		t.Fatalf("the worktree on the Tower: %q, %v; want %q", dir, err, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("the worktree of the Tower has not the code: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) < 3 {
		var names []string
		for _, en := range ents {
			names = append(names, en.Name())
		}
		t.Errorf("the session zero of the table wrote nothing in the worktree of the Tower: %v", names)
	}
	in, err := peers.Inspect(ctx, b.id, ws, table)
	if err != nil || len(in.Dirty) != 0 || in.Tip != git(t, citadelTree, "rev-parse", "HEAD") {
		t.Fatalf("inspection of the worktree of the Tower: %+v, %v", in, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "novo.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if in, err := peers.Inspect(ctx, b.id, ws, table); err != nil || len(in.Dirty) != 1 || in.Dirty[0] != "novo.txt" {
		t.Errorf("the dirty file of the Tower: %+v, %v", in, err)
	}

	// a commit made on the Tower reaches the worktree of the Citadel after the done
	git(t, dir, "add", "novo.txt")
	git(t, dir, "commit", "-q", "-m", "feat: new file")
	rep, err := peers.Done(ctx, b.id, ws, table)
	if err != nil || !rep.Sent {
		t.Fatalf("the done of the Tower: %+v, %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(citadelTree, "novo.txt")); err != nil {
		t.Errorf("the commit of the Tower did not reach the worktree of the Citadel: %v", err)
	}
	// and what a done could not send goes when the connection is back
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "chore: second")
	// the connection drops and comes back: the Tower sends what is missing by itself
	a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	r.waitFor("the second commit to reach the Citadel", func() bool {
		return git(t, citadelTree, "log", "-1", "--format=%s") == "chore: second"
	})

	// what a Tower may not do: tell the end of a done nobody asked, speak of a
	// table where it has no character, send a mirror or a bundle of a table the
	// Citadel does not have
	link := b.e.towerLink()
	ackOf := func(o protocol.Outgoing) protocol.AckResult {
		t.Helper()
		res, err := link.Call(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := ackOf(protocol.Outgoing{Type: protocol.TypeTableSent, Payload: protocol.TableSent{Workspace: ws, Table: table, Sent: true}}); res.Status != protocol.AckError || res.Error == nil || res.Error.Code != protocol.CodeForbidden {
		t.Errorf("a table.sent nobody asked for: %+v", res)
	}
	if res := ackOf(protocol.Outgoing{Type: protocol.TypeTableNotice, Payload: protocol.TableNotice{Workspace: ws, Table: "9-outra", Text: "x"}}); res.Status != protocol.AckError || res.Error == nil || res.Error.Code != protocol.CodeForbidden {
		t.Errorf("a notice about a table where the Tower has no character: %+v", res)
	}
	if res := ackOf(protocol.Outgoing{Type: protocol.TypeTableNotice, Payload: protocol.TableNotice{Workspace: ws, Table: table, Text: "a branch divergiu"}}); res.Status != protocol.AckOK {
		t.Errorf("a notice about a table where the Tower has a character: %+v", res)
	}
	bundle := filepath.Join(root, "x.bundle")
	git(t, src, "bundle", "create", bundle, branch)
	open := func(context.Context) (protocol.Source, func(), error) {
		f, err := os.Open(bundle)
		if err != nil {
			return protocol.Source{}, nil, err
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return protocol.Source{}, nil, err
		}
		return protocol.Source{ReaderAt: f, Size: st.Size()}, func() { _ = f.Close() }, nil
	}
	if err := link.SendBundle(ctx, protocol.Object{"mirror": json.RawMessage(`"meu-app"`), "kind": json.RawMessage(`"mirror"`)}, protocol.BundleSource{Full: open}); err == nil {
		t.Error("the Citadel took the bundle of a mirror from a Tower")
	}
	if err := link.SendBundle(ctx, tables.BundleMeta{Workspace: ws, Table: "99-nada", Branch: "mestre/99-nada", Tip: strings.Repeat("a", 40)}.Object(), protocol.BundleSource{Full: open}); err == nil {
		t.Error("the Citadel took the bundle of a table it does not have")
	}

	// the signs of life of a long command cross the cli.call of the Tower
	call := &localchan.Call{
		Request: localchan.Request{V: 1, ID: "ask-1", AgentID: "c9", Cmd: "ask", Args: localchan.TextArgs("Dev-1", "olá")},
		Caller:  localchan.Caller{Identified: true, AgentID: "c9", TerminalID: "t1"},
	}
	call.SetPartialSink(func(data json.RawMessage) { mu.Lock(); partials = append(partials, string(data)); mu.Unlock() })
	if resp := (forwarder{b.e}).Forward(ctx, call); !resp.OK {
		t.Fatalf("the ask through the Tower: %+v", resp)
	}
	mu.Lock()
	if len(partials) != 2 || partials[0] != `{"status":"queued"}` || partials[1] != `{"status":"delivered"}` {
		t.Errorf("partials = %v", partials)
	}
	mu.Unlock()

	// the Tower removes the worktree of the table, and the Citadel cannot ask what is gone
	if err := peers.RemoveWorktree(ctx, b.id, ws, table); err != nil {
		t.Fatalf("remove the worktree on the Tower: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree of the Tower is still there: %v", err)
	}
	if _, err := peers.Inspect(ctx, b.id, ws, table); !errors.Is(err, tables.ErrNoWorktree) {
		t.Errorf("inspecting a worktree that is gone: %v", err)
	}
	// names that leave the folder are no names
	if _, err := peers.WorktreeDir(ctx, b.id, "../x", table); err == nil {
		t.Error("the Tower answered for a workspace that is a path")
	}

	// with the Tower away, the answer is the one of a machine that cannot be reached
	b.stop()
	r.waitFor("the Citadel to see the Tower gone", func() bool { _, err := peers.Inspect(ctx, b.id, ws, table); return err != nil })
	var unreachable *characters.MachineUnreachableError
	if _, err := peers.Inspect(ctx, b.id, ws, table); !errors.As(err, &unreachable) {
		t.Errorf("with the Tower away: %v", err)
	}
}
