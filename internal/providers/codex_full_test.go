//go:build full

package providers_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// recorder is the character state, the Mestre da mesa and the checklist of
// the supervision, recording what it is told; changed signals each call.
type recorder struct {
	mu      sync.Mutex
	blocked []string
	notices []providers.Notice
	alerts  []providers.Alert
	changed chan struct{}
}

func (r *recorder) signal() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *recorder) Block(_ context.Context, id string, reason providers.BlockReason, _ string) error {
	r.mu.Lock()
	r.blocked = append(r.blocked, id+"/"+string(reason))
	r.mu.Unlock()
	r.signal()
	return nil
}

func (r *recorder) ReduceMonitoring(context.Context, string, providers.ReducedReason, string) error {
	return nil
}

func (r *recorder) Notify(_ context.Context, n providers.Notice) error {
	r.mu.Lock()
	r.notices = append(r.notices, n)
	r.mu.Unlock()
	return nil
}

func (r *recorder) Alert(_ context.Context, a providers.Alert) error {
	r.mu.Lock()
	r.alerts = append(r.alerts, a)
	r.mu.Unlock()
	r.signal()
	return nil
}

func (r *recorder) Confirmed(context.Context, string) error   { return nil }
func (r *recorder) Unconfirmed(context.Context, string) error { return nil }

func (r *recorder) done() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.blocked) > 0 && len(r.alerts) > 0
}

// buildCodexBinaries builds the mestre-pty and the fake agent for one test.
func buildCodexBinaries(t *testing.T) (pty, fake string) {
	t.Helper()
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dir := t.TempDir()
	pty = filepath.Join(dir, "pty", "1.0.0", "mestre-pty"+exe)
	fake = filepath.Join(dir, "codex"+exe) // the fake agent called codex is the fake Codex
	for _, b := range [][]string{
		{"-o", pty, "-ldflags", "-X main.version=1.0.0", "github.com/lucasndas/mestre/cmd/mestre-pty"},
		{"-o", fake, "github.com/lucasndas/mestre/cmd/fakeagent"},
	} {
		if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", b, err, out)
		}
	}
	return pty, fake
}

// CA-CODEX-06
func TestTheHookReviewOfTheFakeCodexBlocksForInputAndAlertsTheChecklist(t *testing.T) {
	t.Parallel()
	ptyBin, fake := buildCodexBinaries(t)
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "mcx")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 2*time.Minute)
	t.Cleanup(cancel)

	// The profile and the worktree as the engine leaves them, without the
	// trust of the hooks: the profile setup never approved them in /hooks.
	a := providers.NewCodex(providers.Options{})
	home, worktree := filepath.Join(root, "c"), filepath.Join(root, "w")
	testkit.WriteFiles(t, root, map[string]string{"w/.git/HEAD": "ref: refs/heads/main\n", "c/auth.json": "{}"})
	if _, err := a.PreTrust(providers.PreTrustRequest{Profile: home, Worktree: worktree}); err != nil {
		t.Fatal(err)
	}
	hooks, err := a.HookFiles(providers.HookFilesRequest{Root: home, Binary: filepath.Join(root, "mestre")})
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, hooks)

	stable := make(chan struct{}, 1)
	sys := terminal.NewSystem()
	db, err := machinedb.Open(ctx, filepath.Join(root, "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	spawner := &terminal.DirectSpawner{Suite: "full", System: sys}
	mgr, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: sys, MachineID: "m1", SocketDir: filepath.Join(root, "s"), LogDir: filepath.Join(root, "l"),
		Spawner:          spawner,
		HandshakeTimeout: time.Minute, // a loaded CI machine can be slow to schedule the mestre-pty it just started
		PTYBinary:        func(string) (string, error) { return ptyBin, nil },
		PTYVersion:       "1.0.0",
		OnEvent: func(ev terminal.Event) {
			if ev.Kind == terminal.EventStable {
				select {
				case stable <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	term, err := mgr.Create(ctx, terminal.CreateSpec{
		ID: "t1", CharacterID: "a1", Dir: worktree, WorkDir: worktree, Path: fake,
		Env: append(os.Environ(), "CODEX_HOME="+home, "HOME="+home, "MESTRE_AGENT_ID=a1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		term.Detach()
		_ = spawner.KillCgroup(context.Background(), "t1")
		_ = sys.KillTree(context.Background(), term.PTY(), nil)
		_ = db.Close()
	})

	r := &recorder{changed: make(chan struct{}, 1)}
	sup := providers.NewSupervisor(providers.SupervisorConfig{
		Clock: clock.Real{}, Adapter: a, AgentID: "a1", TableID: "1-login",
		Keyboard: term, Characters: r, TableMaster: r, Checklist: r, Inbox: r,
	})
	sup.Start(ctx)
	t.Cleanup(sup.Close)
	var screen string
	for !r.done() {
		select {
		case <-stable:
			s, err := term.Screen(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			screen = s.Text
			sup.Screen(screen, true)
		case <-r.changed:
		case <-ctx.Done():
			t.Fatalf("the hook review was not escalated; screen:\n%s", screen)
		}
	}
	if !strings.Contains(screen, "Hooks need review") {
		t.Fatalf("screen:\n%s", screen)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.blocked) != 1 || r.blocked[0] != "a1/input" {
		t.Fatalf("blocked = %v, want a1 blocked with reason input", r.blocked)
	}
	if len(r.alerts) != 1 || r.alerts[0].Item != "ALL-11" || r.alerts[0].AgentID != "a1" {
		t.Fatalf("alerts = %+v, want the checklist alert ALL-11 of a1", r.alerts)
	}
	if len(r.notices) != 1 || r.notices[0].Kind != providers.NoticeBlockedInput {
		t.Fatalf("notices = %+v, want the Mestre da mesa told", r.notices)
	}
}
