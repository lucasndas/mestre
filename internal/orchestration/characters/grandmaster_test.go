package characters_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// usersLog is who the inbox told, as "Sistema".
type usersLog struct {
	mu   sync.Mutex
	told []string
}

func (u *usersLog) NotifyOwner(context.Context, string, string) error { return nil }

func (u *usersLog) NotifyUser(_ context.Context, _, user, text string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.told = append(u.told, user+": "+text)
	return nil
}

func (u *usersLog) all() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.told...)
}

// checkFunc is a TableChecker made of a function, for a rig whose worktrees
// exist only after it is built.
type checkFunc func(ctx context.Context, workspace, table, dir string, p providers.ID) error

func (f checkFunc) CheckStart(ctx context.Context, workspace, table, dir string, p providers.ID) error {
	return f(ctx, workspace, table, dir, p)
}

// Part of CA-GM-17 (the "Sistema" sender is not observable here: inbox.Users carries no sender, the header is the Telegram's): the Grand Master starts through the CheckStart of the session zero
// like every character: with the files of its provider not generated in the
// main clone (and the ones of another provider generated) the start is refused
// with the provider and the problems, the messages of the users stay queued and
// they are told, as "Sistema", that it is unavailable and why; with the files
// corrected it starts.
func TestTheGrandMasterIsRefusedByTheCheckStartWhenTheFilesOfItsProviderWereNotGenerated(t *testing.T) {
	t.Parallel()
	users := &usersLog{}
	var files characters.TableFiles
	var calls []string
	var mu sync.Mutex
	r := newRig(t, rigOptions{stage7: true, users: users, tables: checkFunc(func(ctx context.Context, ws, tbl, dir string, p providers.ID) error {
		mu.Lock()
		calls = append(calls, ws+"/"+tbl+" "+dir+" "+string(p))
		mu.Unlock()
		return files.CheckStart(ctx, ws, tbl, dir, p)
	})})
	main := filepath.Join(r.root, "linux") // the main clone, the table ""
	g := fakegit.NewGit(clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)))
	repo, err := g.Init(r.ctx, main, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	table, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{}), Binary: filepath.Join(t.TempDir(), "mestre")}})
	if err != nil {
		t.Fatal(err)
	}
	files = characters.TableFiles{Table: table, Requests: func(_ context.Context, ws, tbl, dir string) (sessionzero.TableRequest, error) {
		return sessionzero.TableRequest{Workspace: ws, Table: tbl, Dir: dir, Repo: repo}, nil
	}}
	// the file of Claude Code is the person's, and broken: the Mestre does not rewrite it
	broken := filepath.Join(main, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{isto não é json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = r.create(characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster})
	var blocked *sessionzero.BlockedError
	if !errors.As(err, &blocked) || blocked.Provider != providers.ClaudeCode || !strings.Contains(err.Error(), ".claude/settings.local.json") {
		t.Fatalf("the start = %v; want the refusal of claude-code with the problem", err)
	}
	mu.Lock()
	if len(calls) != 1 || calls[0] != workspace+"/ "+main+" claude-code" {
		t.Fatalf("checks = %q; want one, of the main clone, for the provider of the Grand Master", calls)
	}
	mu.Unlock()
	// the files of another provider are free: the refusal holds back only the provider of the Grand Master
	if err := files.CheckStart(r.ctx, workspace, "", main, providers.Codex); err != nil {
		t.Fatalf("CheckStart(codex) = %v; want it free", err)
	}
	gm, ok, err := r.reg.GrandMaster(r.ctx, workspace)
	if err != nil || !ok {
		t.Fatalf("the Grand Master of the workspace = %v, %v; it must stay, interrupted", ok, err)
	}
	if gm.State != characters.Interrupted || !strings.Contains(gm.Reason, "claude-code") || !strings.Contains(gm.Reason, ".claude/settings.local.json") {
		t.Fatalf("the Grand Master is %s (%q); want interrupted with the provider and the problem", gm.State, gm.Reason)
	}
	if ns := r.notes.of(characters.NoticeStartFailed); len(ns) != 1 || ns[0].To.Kind != characters.ToOwner {
		t.Fatalf("notices = %+v; want the Owner told once", ns)
	}

	// the user writes: the message waits in the queue and the user is told why
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "como está o login?", Origin: lucas("")})
	if got := r.itemState(it.ID); got != inbox.Queued {
		t.Fatalf("the message is %s; want it queued", got)
	}
	told := users.all()
	if len(told) != 1 || !strings.HasPrefix(told[0], "lucas: ") || !strings.Contains(told[0], "indisponível") || !strings.Contains(told[0], ".claude/settings.local.json") {
		t.Fatalf("the user was told %q; want that the Grand Master is unavailable and why", told)
	}

	// a second message does not tell the user again
	r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "e agora?", Origin: lucas("")})
	if n := len(users.all()); n != 1 {
		t.Fatalf("the user was told %d times; want once", n)
	}
	if _, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster}); err == nil {
		t.Fatal("a second Grand Master was made")
	}

	// the person fixes the file: the next resumption of the machine starts it
	if err := os.WriteFile(broken, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ResumeMachine(r.ctx, "linux"); err != nil {
		t.Fatalf("the resumption: %v", err)
	}
	if st := r.state(gm); st != characters.Starting {
		t.Fatalf("the Grand Master is %s after the fix; want it starting", st)
	}
	if r.machineOf(gm).host.proc(gm.ID) == nil {
		t.Fatal("no terminal was made for the Grand Master")
	}
}
