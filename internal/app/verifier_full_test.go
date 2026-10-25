//go:build full

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/verification"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The terminal runner of the verification (the proof with a real account, CA-PROVA-09, is M8): the commands of the
// verification run in a terminal of the mestre-pty, in the folder given, and the
// code of the exit, the output and the time limit come back as a result.
func TestTheVerificationRunsACommandInATerminalOfTheMestrePtyAndReadsItsExit(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
	r := newRig(t)
	root, err := os.MkdirTemp("", "mvf")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	a := r.node("a")
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	a.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "marca.txt"), []byte("aqui"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the host composed the executor over the terminals and the database of the Guild
	orc := a.e.orc.Load()
	if ok, err := orc.Verifier.Confirmed(t.Context(), "loja"); err != nil || ok {
		t.Fatalf("before any confirmation: %v, %v", ok, err)
	}
	if err := a.e.withGuildDB(func(db *store.DB) error {
		_, err := verification.Store{DB: db, Clock: clock.Real{}}.Confirm(t.Context(), "loja", "u1", []byte("version: 1\ncomandos: [\"make\"]\n"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := orc.Verifier.Confirmed(t.Context(), "loja"); err != nil || !ok {
		t.Fatalf("with the terminals and a confirmed command: %v, %v", ok, err)
	}
	run := terminalRunner{host: a.e.rt.host, clk: clock.Real{}}
	cat, red := "cat marca.txt", "echo falhou; exit 3"
	if os.PathSeparator == '\\' {
		cat, red = "type marca.txt", "echo falhou& exit /b 3"
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	green, err := run.Run(t.Context(), verification.Run{Dir: work, Home: home, Command: cat, Timeout: time.Minute})
	if err != nil || green.ExitCode != 0 || green.TimedOut || !strings.Contains(green.Output, "aqui") {
		t.Fatalf("a green command = %+v, %v", green, err)
	}
	bad, err := run.Run(t.Context(), verification.Run{Dir: work, Home: home, Command: red, Timeout: time.Minute})
	if err != nil || bad.ExitCode != 3 || bad.TimedOut || !strings.Contains(bad.Output, "falhou") {
		t.Fatalf("a red command = %+v, %v", bad, err)
	}
	if os.PathSeparator != '\\' {
		// no variable of the engine reaches the command; HOME is the scratch folder
		env, err := run.Run(t.Context(), verification.Run{Dir: work, Home: home, Command: "env", Timeout: time.Minute})
		if err != nil || env.ExitCode != 0 || !strings.Contains(env.Output, "HOME="+home) {
			t.Fatalf("the environment of a command = %+v, %v", env, err)
		}
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			switch k {
			case "PATH", "HOME", "PWD", "OLDPWD", "SHLVL", "_", "TERM", "CI", "TMPDIR":
			default:
				if strings.Contains("\n"+strings.ReplaceAll(env.Output, "\r", ""), "\n"+k+"=") {
					t.Errorf("%s of the engine reached the command", k)
				}
			}
		}
		slow, err := run.Run(t.Context(), verification.Run{Dir: work, Home: home, Command: "sleep 60", Timeout: 2 * time.Second})
		if err != nil || !slow.TimedOut {
			t.Fatalf("a command over its time = %+v, %v", slow, err)
		}
	}
}

// A command of a verification that the fall of the engine left running belongs
// to no character: the Reconcile of the host ends it with its tree and forgets
// its row, and hands nothing to the characters.
func TestAVerificationTerminalLeftByAFallIsEndedAndForgottenAtTheNextStart(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
	r := newRig(t)
	root, err := os.MkdirTemp("", "mvg")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	a := r.node("a")
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	a.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })

	host := a.e.rt.host
	id := characters.VerificationTerminalPrefix + "fall"
	sleeper := "sleep 300"
	if os.PathSeparator == '\\' {
		sleeper = "ping -n 300 127.0.0.1"
	}
	path, args := shellFor(runtime.GOOS, sleeper) // the shell the verification itself uses
	tm, err := host.Manager().Create(t.Context(), terminal.CreateSpec{ID: id, Dir: root, Path: path, Args: args, Env: verifyEnv(runtime.GOOS, root, os.Getenv), WorkDir: root})
	if err != nil {
		t.Fatal(err)
	}
	tm.Detach() // the engine "fell": the process stays, nobody holds it
	rep, err := host.Reconcile(t.Context())
	if err != nil || len(rep.Reconnected) != 0 || len(rep.Interrupted) != 0 {
		t.Fatalf("Reconcile = %+v, %v: a verification terminal was handed to the characters", rep, err)
	}
	rows, _ := host.Manager().Records(t.Context())
	for _, row := range rows {
		if row.ID == id {
			t.Errorf("the row of the left terminal stayed: %+v", row)
		}
	}
}

// Part of CA-GH-ACOES-11 (the chain half, with a double of the verifier, is in
// internal/orchestration/chain): the real executor integrates the base in an
// ephemeral worktree: the command runs in a folder of the verifications that holds
// the base merged, and afterwards neither the folder nor the branch of the
// verification is left, and the branch of the table has not moved.
func TestTheVerificationIntegratesTheBaseInAnEphemeralWorktreeThatIsGoneAfterwards(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	mirror := rg.mirror(t)
	branch := rg.opened.Table.Branch
	head := rg.commitOn(t, branch, "feature.txt", "table\n")
	base := rg.commitOn(t, "main", "base.txt", "base\n")
	store := verification.Store{DB: rg.db, Clock: rg.clk}
	if _, err := store.Confirm(ctx, rg.ws.Slug, "u1", []byte("version: 1\ncomandos: [\"make check\"]\n")); err != nil {
		t.Fatal(err)
	}
	var ranIn string
	var baseMerged bool
	run := &recordingRunner{}
	run.seen = func(r verification.Run) {
		ranIn = r.Dir
		refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre")
		if len(refs) == 1 {
			b, err := mirror.ReadFile(ctx, refs[0].Name, "base.txt")
			baseMerged = err == nil && string(b) == "base\n"
		}
	}
	ex := &verification.Executor{Store: store, Worktrees: rg.w, Runner: run}
	res, err := ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch, Head: string(head), Base: string(base)})
	if err != nil || !res.OK {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
	if want := rg.orc.Workspaces.Layout().Verifications(rg.ws.Slug); filepath.Dir(ranIn) != want {
		t.Errorf("the command ran in %q, want a folder of %s (ephemeral)", ranIn, want)
	}
	if !baseMerged {
		t.Error("the base was not merged in the worktree the command ran in")
	}
	if _, err := os.Stat(ranIn); err == nil {
		t.Error("the ephemeral worktree stayed after the verification")
	}
	if refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre"); len(refs) != 0 {
		t.Errorf("the branch of the verification stayed: %v", refs)
	}
	if now, _ := mirror.ResolveRef(ctx, "refs/heads/"+branch); now != head {
		t.Errorf("the branch of the table moved from %s to %s", head, now)
	}
}
