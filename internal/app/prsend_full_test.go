//go:build full

package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// prWorld is the Citadel and a Tower of two real engines with a table that has
// a worktree on each, and GitHub behind the commands `pr` and `issue` as mestre
// run puts it: the standard integration over the double of GitHub (the Apps
// "Grão-Mestre" and "Mestre" ready) and a bare repository where the branch is
// sent.
type prWorld struct {
	t      *testing.T
	r      *rig
	a, b   *inproc
	root   string
	ws     string
	table  string
	branch string
	epic   int

	mirror, citadelTree, towerTree, github string
	gh                                     *fakegithub.Server
	url                                    string
	// calls counts every request that reached the double of GitHub.
	calls  atomic.Int64
	run    *stdRun
	fwd    *localchan.LocalForwarder
	master characters.Character
}

const (
	prWS     = "meu-app"
	prTable  = "3-login"
	prBranch = "mestre/3-login"
	prRepo   = "lucas/meu_app"
)

func newPRWorld(t *testing.T) *prWorld {
	t.Helper()
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrp")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	w := &prWorld{t: t, r: r, root: root, ws: prWS, table: prTable, branch: prBranch}
	w.a = r.node("a")
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

	// GitHub: the repository, a bare repository where the branch is sent, and the epic
	w.github = filepath.Join(root, "github.git")
	git(t, root, "init", "-q", "--bare", w.github)
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
	git(t, src, "branch", w.branch)
	main := git(t, src, "rev-parse", "main")
	w.mirror = filepath.Join(w.a.paths.Workspaces, w.ws, "espelho.git")
	if err := os.MkdirAll(filepath.Dir(w.mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", "--bare", src, w.mirror)
	git(t, w.mirror, "update-ref", "refs/remotes/origin/main", main)
	w.citadelTree = filepath.Join(w.a.paths.Workspaces, w.ws, "mesas", w.table)
	git(t, w.mirror, "worktree", "add", "-q", w.citadelTree, w.branch)

	orc := w.a.e.orc.Load()
	ctx := t.Context()
	db := orcDB(t, w.a)
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu_app', 'lucas/meu_app', ?, 'main', 'active', '', 1, 1)`, w.ws, w.github); err != nil {
		t.Fatal(err)
	}
	gh, err := fakegithub.New(clock.Real{}, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu_app")
	w.gh = gh
	w.epic = gh.AddIssue(prRepo, "Login de usuários", "epico")
	now := clock.Real{}.Now()
	if err := orc.Tables.Store().Insert(ctx, tables.Table{Workspace: w.ws, ID: w.table, Epic: w.epic, EpicTitle: "Login", Branch: w.branch, BaseBranch: "main", BaseCommit: main,
		State: tables.StateWorking, OpenedBy: "engine", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	g, err := gitx.New(gitx.Options{Clock: clock.Real{}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, w.citadelTree)
	if err != nil {
		t.Fatal(err)
	}
	if err := orc.Tables.Store().PutWorktree(ctx, tables.Worktree{Workspace: w.ws, Table: w.table, Machine: w.a.id, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common,
		LastCommit: git(t, w.citadelTree, "rev-parse", "HEAD"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// the Master of the table lives on the Citadel and a character of the table on the Tower
	w.master = characters.Character{ID: "m3", Name: "Mestre-3", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: w.ws, Table: w.table, Machine: w.a.id,
		Sheet: "Mestre da mesa", Base: "claude-code", Class: "mestre-da-mesa", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: now}
	live := characters.Character{ID: "c9", Name: "Dev-9", Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: w.ws, Table: w.table, Machine: w.b.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral, CreatedAt: now}
	for _, c := range []characters.Character{w.master, live} {
		if err := orc.Registry.Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := orc.Tables.SyncTower(ctx, w.ws, w.table, w.b.id); err != nil {
		t.Fatalf("the sync of the table to the Tower: %v", err)
	}
	peers := towerTables{w.a.e.towerClient()}
	if w.towerTree, err = peers.WorktreeDir(ctx, w.b.id, w.ws, w.table); err != nil {
		t.Fatal(err)
	}
	w.githubReady(orc, db)
	return w
}

// githubReady makes the Apps ready at the double of GitHub and puts the
// standard integration on the orchestration of the Citadel with the lease of the
// epoch valid.
func (w *prWorld) githubReady(orc *Orchestration, db *store.DB) {
	t := w.t
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.calls.Add(1)
		w.gh.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	w.url = srv.URL
	ctx := t.Context()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	apps := &github.Registry{DB: db, Clock: clock.Real{}}
	v := w.a.e.cit.vault
	for n, a := range []github.App{{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, {Name: "lucas-mestre-mestre", Role: github.RoleMestre}} {
		a.Slug = github.Slug(a.Name)
		if _, err := apps.Request(ctx, a); err != nil {
			t.Fatal(err)
		}
		appID, inst := int64(1+n), int64(10+n)
		if err := apps.Created(ctx, a.Name, a.Slug, appID, "Iv1."+a.Slug); err != nil {
			t.Fatal(err)
		}
		if err := apps.BotID(ctx, a.Name, int64(100+n)); err != nil {
			t.Fatal(err)
		}
		if err := apps.Installed(ctx, a.Name, inst, []string{prRepo}); err != nil {
			t.Fatal(err)
		}
		w.gh.AddApp(a.Slug, appID, inst, &key.PublicKey, false, prRepo)
		w.gh.AddBot(a.Slug, int64(100+n))
		if err := v.Put(ctx, github.KeySecret(a.Name), vault.TypeGitHubAppKey, pemKey, nil); err != nil {
			t.Fatal(err)
		}
	}
	fence := guild.NewFence(guild.FenceConfig{Lock: epochLock{}, Clock: clock.Real{}, Epoch: func() uint64 { return 1 }})
	go func() { _ = fence.Run(ctx) }()
	w.attach(orc, db, v, fence)
	w.r.waitFor("the lease of the epoch to be valid", fence.LeaseValid)
}

// attach opens the standard integration over the double of GitHub for the
// period of the orchestration and has the commands of GitHub act under the fence.
func (w *prWorld) attach(orc *Orchestration, db *store.DB, v *vault.Vault, fence *guild.Fence) {
	t := w.t
	std := StandardIntegration{GitHubBase: w.url, GitHubWeb: w.url, HTTP: &http.Client{Timeout: 10 * time.Second}}
	run, err := std.Open(t.Context(), IntegrationEnv{DB: db, Vault: v, Clock: clock.Real{}, Machine: w.a.id, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	w.run = run.(*stdRun)
	w.fwd = localchan.NewLocalForwarder()
	w.run.Attach(orc, w.fwd)
	w.run.BindFence(fence)
}

// ask is a command of the Master of the table through the local channel.
func (w *prWorld) ask(cmd string, args []string, flags map[string]any) localchan.Response {
	w.t.Helper()
	return w.fwd.Forward(w.t.Context(), &localchan.Call{Request: localchan.Request{V: 1, ID: "op-" + strings.ReplaceAll(cmd, " ", "-"), AgentID: w.master.ID, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller: localchan.Caller{Identified: true, AgentID: w.master.ID}})
}

// branches lists the branches of the bare repository that stands for GitHub.
func (w *prWorld) branches() []string {
	out := git(w.t, w.github, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// pulls counts the pull requests the double of GitHub was asked to open.
func (w *prWorld) pulls() int {
	n := 0
	for _, a := range w.gh.Actions() {
		if a.Method == http.MethodPost && strings.HasSuffix(a.Path, "/pulls") {
			n++
		}
	}
	return n
}

// CA-GH-ACOES-06
func TestPROpenSendsTheCommitsOfBothMachinesAsOneBranchToGitHub(t *testing.T) {
	t.Parallel()
	w := newPRWorld(t)
	ctx := t.Context()
	// a commit made on the Citadel, one made on the Tower after it had the first, and the done of the Tower
	if err := os.WriteFile(filepath.Join(w.citadelTree, "citadel.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w.citadelTree, "add", "citadel.txt")
	git(t, w.citadelTree, "commit", "-q", "-m", "feat: from the citadel")
	orc := w.a.e.orc.Load()
	if err := orc.Tables.SyncTower(ctx, w.ws, w.table, w.b.id); err != nil {
		t.Fatalf("the sync of the table to the Tower: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.towerTree, "citadel.txt")); err != nil {
		t.Fatalf("the Tower did not get the commit of the Citadel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.towerTree, "tower.txt"), []byte("t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w.towerTree, "add", "tower.txt")
	git(t, w.towerTree, "commit", "-q", "-m", "feat: from the tower")
	peers := towerTables{w.a.e.towerClient()}
	if rep, err := peers.Done(ctx, w.b.id, w.ws, w.table); err != nil || !rep.Sent {
		t.Fatalf("the done of the Tower: %+v, %v", rep, err)
	}
	if got := git(t, w.mirror, "log", "--format=%s", w.branch); !strings.Contains(got, "feat: from the tower") || !strings.Contains(got, "feat: from the citadel") {
		t.Fatalf("the mirror has %q; want the commits of both machines before the sending", got)
	}
	if len(w.branches()) != 0 {
		t.Fatalf("something reached GitHub before the Master asked: %v", w.branches())
	}

	// the Master of the table opens the pull request
	got := w.ask("pr open", nil, map[string]any{"title": "Login"})
	if !got.OK {
		t.Fatalf("pr open = %+v", got.Error)
	}
	if bs := w.branches(); len(bs) != 1 || bs[0] != w.branch {
		t.Fatalf("GitHub got the branches %v; want only %q", bs, w.branch)
	}
	log := git(t, w.github, "log", "--format=%s", w.branch)
	for _, want := range []string{"feat: from the citadel", "feat: from the tower", "chore: initial commit"} {
		if !strings.Contains(log, want) {
			t.Errorf("the branch on GitHub lacks %q:\n%s", want, log)
		}
	}
	if sent, mirror := git(t, w.github, "rev-parse", w.branch), git(t, w.mirror, "rev-parse", w.branch); sent != mirror {
		t.Errorf("GitHub has %s, the mirror %s: not the branch the mirror holds", sent, mirror)
	}
	if n := w.pulls(); n != 1 {
		t.Errorf("%d pull requests were opened; want one", n)
	}
	// the token of the push is the App's, for the repository, limited to what the sending needs
	var pushed bool
	for _, tr := range w.gh.TokenRequests() {
		if tr.Permissions["contents"] == "write" {
			pushed = true
			if _, extra := tr.Permissions["workflows"]; extra || len(tr.Repositories) != 1 {
				t.Errorf("the token of the push = %+v; want contents:write for the repository and no workflows", tr)
			}
		}
	}
	if !pushed {
		t.Error("no token with the permission to write contents was made for the push")
	}
}

// CA-GH-ACOES-09
// This is also the condition CA-CIDADELA-06 and CA-CIDADELA-07 ask besides the
// rest of their scene: the double of GitHub gets no call from the old Citadel.
func TestAnOldCitadelBackAfterAPromotionSendsNothingToGitHubStepsDownAndTheRefusalIsAudited(t *testing.T) {
	t.Parallel()
	w := newPRWorld(t)
	r, ctx := w.r, t.Context()
	lsn := w.a.mark("t1")
	r.waitFor("b to apply t1", func() bool { return w.b.connected(1) && w.b.e.Status().LSN >= lsn })

	// the Citadel stops and the Tower is promoted: the epoch of the Guild is 2
	w.a.stop()
	if err := w.b.e.Promote(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	r.waitFor("b to take the command", func() bool {
		st := w.b.e.Status()
		return st.Role == RoleCitadel && st.Epoch == 2
	})

	// the old Citadel comes back, and until its first check of the lock it still
	// holds the epoch 1: a request to comment on an issue waits in its fence
	w.a.cfg.Addresses = []string{net.JoinHostPort(w.b.name, "7443")} // where the new Citadel is
	r.lock.Hold()
	defer r.lock.Release()
	r.start(w.a, clock.Real{})
	r.waitFor("a to be back, suspended and hosting the orchestration", func() bool {
		return w.a.e.Status().Fence == guild.FenceSuspended && w.a.e.orc.Load() != nil && w.a.e.cit != nil
	})
	orc, run := w.a.e.orc.Load(), w.a.e.cit
	w.attach(orc, run.db, run.vault, run.fence)
	before := w.calls.Load()
	got := w.ask("issue comment", []string{"3", "feito pela Cidadela antiga"}, nil)
	if !got.OK || !strings.Contains(string(got.Data), "queued") {
		t.Fatalf("issue comment on the suspended Citadel = %+v, want it queued", got)
	}

	// the lock shows the epoch 2: the machine steps down to a Tower and the request is dropped
	r.lock.Release()
	r.waitFor("a to step down to a Tower of the epoch 2", func() bool { return w.a.connected(2) })
	if n := w.calls.Load() - before; n != 0 {
		t.Fatalf("the double of GitHub got %d calls from the old Citadel; want none", n)
	}
	for _, a := range w.gh.Actions() {
		if a.Method != http.MethodGet && strings.Contains(a.Path, "comments") {
			t.Errorf("the double of GitHub accepted %s %s from the old Citadel", a.Method, a.Path)
		}
	}
	// the refusal is in the audit of the Guild
	r.waitFor("the new Citadel to audit the dropped request", func() bool {
		rows := w.b.query(`SELECT detail FROM audit_log WHERE action = 'citadel.dropped_actions'`)
		return len(rows) == 1 && strings.Contains(rows[0], "issue comment")
	})
}
