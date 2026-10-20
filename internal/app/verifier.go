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
	id := "verifica-" + hex.EncodeToString(b[:])
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
	if exit.Signal != "" && code == 0 {
		code = -1 // ended by a signal: never a green
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
	name := "verifica-" + hex.EncodeToString(b[:])
	tables := orc.Workspaces.Layout().Tables(req.Workspace)
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
	// the worktree is made at the commit that was read, not at the name of the branch
	_, err = mirror.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: branch, Create: true, StartPoint: string(tip)})
	if err != nil {
		remove() // the branch may have been made before the failure
		return verification.Tree{}, nil, err
	}
	// written by the account of the agents through the group of the folder of
	// the tables; no chmod, which a link planted there could turn on another folder
	if err := os.MkdirAll(tables, 0o770); err != nil {
		remove()
		return verification.Tree{}, nil, err
	}
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
