package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/verification"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The verification of the done of a class with verify: true
// (docs/orquestracao/prova-e-devolucao.md#o-done-que-a-classe-exige): the
// confirmed commands of the workspace (verification.Executor) in an ephemeral
// worktree of the table, one terminal of the mestre-pty each, so the account that
// runs them is the one the launcher starts the mestre-pty as, never the engine's.
// A machine with no terminals runs nothing, and the step ends unverified.

// terminalRunner is verification.Runner over the terminals of the machine: a
// terminal with no character, whose process is the shell of the command.
type terminalRunner struct {
	host *characters.TerminalHost
	clk  clock.Clock
}

var _ verification.Runner = terminalRunner{}

// shellFor is the shell and the arguments that run one command line.
func shellFor(goos, command string) (string, []string) {
	if goos == "windows" {
		return "cmd.exe", []string{"/d", "/c", command}
	}
	return "/bin/sh", []string{"-c", command}
}

// verifyEnv is the whole environment of a command of the verification: the
// terminal gets it as it is (the mestre-pty does not add the one of the engine),
// so no variable of the engine, and no secret of the vault or the shell that
// started it, reaches a command. It has the program search path, the system
// variables Windows needs to start a shell, and a HOME and a temporary folder
// that are the scratch folder of this verification.
func verifyEnv(goos, home string, getenv func(string) string) []string {
	env := []string{"CI=1", "TERM=dumb"}
	if p := getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	if home != "" {
		env = append(env, "HOME="+home, "TMPDIR="+home)
		if goos == "windows" {
			env = append(env, "USERPROFILE="+home, "TEMP="+home, "TMP="+home)
		}
	}
	if goos == "windows" && home != "" {
		env = append(env, "LOCALAPPDATA="+home, "APPDATA="+home, "HOMEPATH="+home, "ProgramData="+home)
	}
	if goos == "windows" {
		for _, k := range []string{"SystemRoot", "windir", "ComSpec", "PATHEXT", "SystemDrive"} {
			if v := getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
	}
	return env
}

func (r terminalRunner) Run(ctx context.Context, run verification.Run) (verification.Result, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return verification.Result{}, err
	}
	id := characters.VerificationTerminalPrefix + hex.EncodeToString(b[:])
	path, args := shellFor(runtime.GOOS, run.Command)
	env := verifyEnv(runtime.GOOS, run.Home, os.Getenv)
	t, err := r.host.Manager().Create(ctx, terminal.CreateSpec{ID: id, Dir: run.Dir, Path: path, Args: args, Env: env, WorkDir: run.Dir})
	if err != nil {
		return verification.Result{}, err
	}
	timer := r.clk.NewTimer(run.Timeout)
	defer timer.Stop()
	timedOut := false
	select {
	case <-t.Exited():
	case <-t.Client().Done():
		// the connection to the mestre-pty ended: an exit that arrived with it is
		// the result, anything else is a command that cannot be said to have passed
		select {
		case <-t.Exited():
		default:
			r.stop(t)
			return verification.Result{}, errors.New("the connection to the terminal of the command was lost")
		}
	case <-timer.C():
		timedOut = true
	case <-ctx.Done():
		r.stop(t)
		return verification.Result{}, ctx.Err()
	}
	if err := t.WaitLogged(ctx, t.Client().Exit().End); err != nil && !timedOut {
		r.log(err)
	}
	exit := t.Client().Exit()
	// the end of the output is what is kept: read from near the end, not from the start
	from := uint64(0)
	if exit.End > verifyOutputRead {
		from = exit.End - verifyOutputRead
	}
	rep, rerr := t.Replay(ctx, from)
	r.stop(t)
	if rerr != nil {
		return verification.Result{}, fmt.Errorf("the output of the command could not be read: %w", rerr)
	}
	code := exit.Code
	if (exit.Signal != "" || exit.NoCode) && code == 0 {
		code = -1 // ended by a signal, or with no code announced: never a green
	}
	return verification.Result{ExitCode: code, Output: string(rep.Data), TimedOut: timedOut}, nil
}

// verifyOutputRead is how much of the end of the output is read.
const verifyOutputRead = 256 << 10

func (r terminalRunner) log(error) {}

// stop ends the terminal and its tree; it does not depend on the context of the
// caller, which may be the one that ended.
func (r terminalRunner) stop(t *terminal.Terminal) {
	ctx, cancel := clock.WithTimeout(context.Background(), r.clk, 30*time.Second)
	defer cancel()
	_, _ = t.Stop(ctx, terminal.StopPlan{})
}

// mirrorWorktrees is verification.Worktrees over the mirror of the workspace: a
// worktree on a branch of its own at the tip of the branch of the table, in the
// folder of the tables so the account of the agents reaches it like the others.
type mirrorWorktrees struct {
	orc func() *Orchestration
	git tables.Git
	clk clock.Clock
	log *slog.Logger
}

var _ verification.Worktrees = mirrorWorktrees{}

// verifyBoot starts the name of every ephemeral worktree of this run of the
// engine ("verifica-<run>-"): the sweep of the start removes what other runs
// left and never what a verification of this one is using.
var verifyBoot = func() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "verifica-" + hex.EncodeToString(b[:]) + "-"
}()

// mergeIdentity signs the merge of the base in the worktree: it is never
// pushed, and the branch of the table is not moved by it.
var mergeIdentity = gitx.Signature{Name: "Mestre", Email: "mestre@localhost.invalid"}

func (w mirrorWorktrees) Add(ctx context.Context, req verification.TreeRequest) (verification.Tree, func(), error) {
	orc := w.orc()
	if orc == nil || orc.Workspaces == nil {
		return verification.Tree{}, nil, errors.New("app: no workspaces to make a worktree in")
	}
	if req.Head != "" {
		if err := gitx.ValidateCommit(req.Head); err != nil {
			return verification.Tree{}, nil, fmt.Errorf("the commit to prove: %w", err)
		}
	}
	if req.Base != "" {
		if err := gitx.ValidateCommit(req.Base); err != nil {
			return verification.Tree{}, nil, fmt.Errorf("the base: %w", err)
		}
	}
	mirror, err := orc.Workspaces.OpenMirror(ctx, req.Workspace)
	if err != nil {
		return verification.Tree{}, nil, err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/heads/"+req.Branch)
	if err != nil {
		return verification.Tree{}, nil, fmt.Errorf("the branch of the table %s is not in the mirror: %w", req.Table, err)
	}
	if req.Head != "" && string(tip) != req.Head {
		return verification.Tree{}, nil, fmt.Errorf("%w: %s is at %.8s, not at %.8s", verification.ErrHeadMoved, req.Branch, tip, req.Head)
	}
	if req.Base != "" {
		if _, err := mirror.ResolveRef(ctx, req.Base); err != nil {
			return verification.Tree{}, nil, fmt.Errorf("the base %.8s is not in the mirror: %w", req.Base, err)
		}
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return verification.Tree{}, nil, err
	}
	name := verifyBoot + hex.EncodeToString(b[:])
	tables := orc.Workspaces.Layout().Verifications(req.Workspace)
	dir, home := filepath.Join(tables, name), filepath.Join(tables, name+".home")
	branch := "mestre/" + name
	remove := func() {
		c, cancel := clock.WithTimeout(context.WithoutCancel(ctx), w.clk, time.Minute)
		defer cancel()
		// what the commands left may not be the engine's to delete: it is told, not hidden
		for what, err := range map[string]error{"worktree": mirror.RemoveWorktree(c, dir, true), "branch": mirror.DeleteBranch(c, branch), "home": os.RemoveAll(home)} {
			if err != nil && !errors.Is(err, gitx.ErrNoSuchWorktree) && !errors.Is(err, gitx.ErrRefNotFound) {
				w.log.Warn("verification: the ephemeral "+what+" was not removed", "dir", dir, "err", err)
			}
		}
	}
	// verificacoes/ is made here, before git does it with whatever mode it has, and
	// it must be a real folder: what lets the account of the agents into it is the
	// installation's, as for mesas/ (the group and the ACL of the folders of the workspace)
	if err := os.MkdirAll(tables, 0o770); err != nil {
		return verification.Tree{}, nil, err
	}
	if st, err := os.Lstat(tables); err != nil || !st.IsDir() {
		return verification.Tree{}, nil, fmt.Errorf("%s is not a folder", tables)
	}
	// the worktree is made at the commit that was read, not at the name of the branch
	_, err = mirror.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: branch, Create: true, StartPoint: string(tip)})
	if err != nil {
		remove() // the branch may have been made before the failure
		return verification.Tree{}, nil, err
	}
	// no chmod, which a link planted there could turn on another folder
	if err := os.Mkdir(home, 0o770); err != nil {
		remove()
		return verification.Tree{}, nil, err
	}
	if req.Base != "" {
		// the merge of the base is made in the worktree, on its own branch: the
		// branch of the table does not move
		// opened by the record taken now, never by the .git an agent can rewrite
		// (docs/arquitetura/seguranca.md#git-do-motor)
		rec, err := w.git.Record(ctx, dir)
		if err != nil {
			remove()
			return verification.Tree{}, nil, err
		}
		wt, err := w.git.OpenWorktree(ctx, rec)
		if err != nil {
			remove()
			return verification.Tree{}, nil, err
		}
		who := mergeIdentity
		who.When = w.clk.Now()
		if _, err := wt.Merge(ctx, req.Base, who); err != nil {
			remove()
			if errors.Is(err, gitx.ErrMergeConflict) {
				return verification.Tree{}, nil, fmt.Errorf("%w: %.8s into %s: %v", verification.ErrBaseConflict, req.Base, req.Branch, err)
			}
			return verification.Tree{}, nil, fmt.Errorf("the base %.8s could not be integrated: %w", req.Base, err)
		}
	}
	return verification.Tree{Dir: dir, Home: home}, remove, nil
}

// verifier is the executor of the period, or nil when the machine has no terminals.
func (e *Engine) verifier(db *store.DB, git tables.Git, orc func() *Orchestration) *verification.Executor {
	ex := &verification.Executor{Store: verification.Store{DB: db, Clock: e.clk}, Worktrees: mirrorWorktrees{orc: orc, git: git, clk: e.clk, log: e.log}, Mask: e.deps.Masker.Mask}
	if e.rt != nil {
		ex.Runner = terminalRunner{host: e.rt.host, clk: e.clk}
	}
	return ex
}

// sweepVerifications ends what a fall of the engine left of the verifications:
// the ephemeral worktrees and the folders of verificacoes/, and the branches
// mestre/verifica-* of the mirrors. (The terminals verifica-* are ended and
// forgotten by the Reconcile of the host.) It runs when the period starts, // of the runs before this one (a verification of this run is left alone, whenever
// the sweep runs); what cannot be removed is told, not hidden.
func ours(name string) bool { return strings.HasPrefix(name, verifyBoot) }

func (o *Orchestration) sweepVerifications(ctx context.Context) {
	list, err := o.Workspaces.List(ctx)
	if err != nil {
		o.log.Warn("verification: the workspaces were not listed for the sweep", "err", err)
		return
	}
	for _, ws := range list {
		dir := o.Workspaces.Layout().Verifications(ws.Slug)
		mirror, err := o.Workspaces.OpenMirror(ctx, ws.Slug)
		if err == nil {
			if wts, err := mirror.ListWorktrees(ctx); err == nil {
				for _, wt := range wts {
					// the path comes from a file an agent can write: it is a verification only
					// if its branch and its name say so too
					if filepath.Clean(filepath.Dir(wt.Path)) == filepath.Clean(dir) && !ours(filepath.Base(wt.Path)) &&
						strings.HasPrefix(filepath.Base(wt.Path), "verifica-") && strings.HasPrefix(wt.Branch, "mestre/verifica-") {
						if err := mirror.RemoveWorktree(ctx, wt.Path, true); err != nil && !errors.Is(err, gitx.ErrNoSuchWorktree) {
							o.log.Warn("verification: a worktree left by a fall was not removed", "dir", wt.Path, "err", err)
						}
					}
				}
			}
		}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !strings.HasPrefix(e.Name(), "verifica-") || ours(e.Name()) {
					continue
				}
				if p := filepath.Join(dir, e.Name()); os.RemoveAll(p) != nil {
					o.log.Warn("verification: a folder left by a fall was not removed", "dir", p)
				}
			}
		}
		if mirror == nil {
			continue
		}
		refs, err := mirror.ListRefs(ctx, "refs/heads/mestre/")
		if err != nil {
			continue
		}
		for _, r := range refs {
			if !strings.HasPrefix(r.Name, "refs/heads/mestre/verifica-") || ours(strings.TrimPrefix(r.Name, "refs/heads/mestre/")) {
				continue
			}
			if err := mirror.DeleteBranch(ctx, strings.TrimPrefix(r.Name, "refs/heads/")); err != nil {
				o.log.Warn("verification: a branch left by a fall was not removed", "branch", r.Name, "err", err)
			}
		}
	}
}
