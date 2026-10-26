//go:build full

package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/testkit/orcfakes"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// builderIntegration gives a Citadel the session zero of a character with the
// least it needs: the real one asks for the decrees, the identities and the
// classes of a complete run, which the test of the channel does not have.
type builderIntegration struct{ resumed *resumeLog }

func (b builderIntegration) Open(context.Context, IntegrationEnv) (IntegrationRun, error) {
	return builderRun(b), nil
}

// resumeLog keeps the session each character was built to resume (empty: a new one).
type resumeLog struct {
	mu   sync.Mutex
	byID map[string][]string
}

func (l *resumeLog) add(id, session string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil {
		l.byID = map[string][]string{}
	}
	l.byID[id] = append(l.byID[id], session)
}

// of is the sessions the character was built with, in order.
func (l *resumeLog) of(id string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.byID[id])
}

// recordingBuilder is the session zero of the test, which says what each build resumes.
type recordingBuilder struct {
	orcfakes.Builder
	log *resumeLog
}

func (b recordingBuilder) Build(ctx context.Context, in characters.BuildInput) (sessionzero.CharacterRequest, error) {
	if b.log != nil {
		b.log.add(in.Character.ID, in.ResumeSessionID)
	}
	return b.Builder.Build(ctx, in)
}

type builderRun struct{ resumed *resumeLog }

func (b builderRun) Extend(o *OrchestrationOptions) { o.Builder = recordingBuilder{log: b.resumed} }
func (builderRun) Attach(*Orchestration, *localchan.LocalForwarder) []ExternalFunc {
	return nil
}
func (builderRun) BindFence(*guild.Fence)        {}
func (builderRun) FenceChanged(guild.FenceState) {}
func (builderRun) Close()                        {}

// seedTable gives the Citadel a table of the workspace as the opening makes it: a
// mirror with the branch, the worktree of the Citadel and the rows that say so.
func (x *channelRig) seedTable(t *testing.T, table, branch string, epic int) (citadelTree string) {
	t.Helper()
	const ws = "meu-app"
	src := filepath.Join(x.root, "src")
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
	mirror := filepath.Join(x.a.paths.Workspaces, ws, "espelho.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, x.root, "clone", "-q", "--bare", src, mirror)
	citadelTree = filepath.Join(x.a.paths.Workspaces, ws, "mesas", table)
	git(t, mirror, "worktree", "add", "-q", citadelTree, branch)
	now := clock.Real{}.Now()
	ctx := t.Context()
	if err := x.orc.Tables.Store().Insert(ctx, tables.Table{Workspace: ws, ID: table, Epic: epic, EpicTitle: "Busca", Branch: branch, BaseBranch: "main", BaseCommit: git(t, src, "rev-parse", "HEAD"),
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
	if err := x.orc.Tables.Store().PutWorktree(ctx, tables.Worktree{Workspace: ws, Table: table, Machine: x.a.id, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common,
		LastCommit: git(t, src, "rev-parse", branch), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return citadelTree
}

// The recruitment of a character on a Tower, from the command of the table master to
// the agent: the Citadel syncs the table to the Tower (worktree with the session zero),
// the Tower prepares and starts the terminal, the state comes back in the reports, a
// message of the inbox is typed there, the turn that ends brings the character back
// to idle, and dismissing it ends the terminal on the Tower.
// Part of CA-CIDADELA-16 (the whole path of a character on a Tower; the other cases of the criterion are in towerchannel_full_test.go)
func TestAMasterRecruitsACharacterOnATowerAndItWorksTheTableThere(t *testing.T) {
	t.Parallel()
	x := newChannelRigWith(t, channelOpts{builder: true})
	ctx := t.Context()
	const table = "4-busca"
	x.seedTable(t, table, "mestre/4-busca", 4)
	now := clock.Real{}.Now()
	master := characters.Character{ID: "m4", Name: "Mestre", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: "meu-app", Table: table, Machine: x.a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: now}
	if err := x.orc.Registry.Insert(ctx, master); err != nil {
		t.Fatal(err)
	}
	resp := forwarder{x.a.e}.Forward(ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "r1", AgentID: master.ID, Cmd: "recruit", Args: localchan.TextArgs("Dev-Busca"), Flags: map[string]any{"sheet": "Dev Claude", "machine": x.b.id}},
		Caller:  localchan.Caller{Identified: true, AgentID: master.ID},
	})
	if !resp.OK {
		t.Fatalf("recruit: %+v", resp.Error)
	}
	var dev characters.Character
	x.r.waitFor("the character to be ready on the Tower", func() bool {
		cs, err := x.orc.Registry.AliveOf(ctx, "meu-app")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.Name == "Dev-Busca" {
				dev = c
			}
		}
		return dev.ID != "" && x.state(t, dev.ID) == characters.Idle
	})
	if dev.Machine != x.b.id || dev.Table != table {
		t.Fatalf("the character is on %q of table %q; want the Tower and %s", dev.Machine, dev.Table, table)
	}
	tree := filepath.Join(x.b.paths.Workspaces, "meu-app", "mesas", table)
	if _, err := os.Stat(filepath.Join(tree, "README.md")); err != nil {
		t.Errorf("the worktree of the table on the Tower has not the code: %v", err)
	}
	if branch := git(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); branch != "mestre/4-busca" {
		t.Errorf("the worktree of the Tower is on %q; want the branch of the table", branch)
	}
	if !x.aliveOnTower(dev.ID) {
		t.Fatal("the Tower has no live terminal for the character")
	}
	// a message of the inbox is typed on the Tower; the turn ends and the Citadel sees it idle again
	before := dev.LastSignalAt
	it, err := x.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: dev.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: "implemente a busca"})
	if err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the message to be delivered and the turn to end", func() bool {
		cur, err := x.orc.Inbox.Store().Get(ctx, it.ID)
		now, gerr := x.orc.Registry.Get(ctx, dev.ID)
		// the signals of the turn (start and end) came back from the Tower: it went through busy
		return err == nil && gerr == nil && cur.State == inbox.Delivered && now.State == characters.Idle && now.LastSignalAt.After(before)
	})
	// the task of the message is still open: dismissing is scheduled for its safe point
	dismiss := func() localchan.Response {
		return forwarder{x.a.e}.Forward(ctx, &localchan.Call{
			Request: localchan.Request{V: 1, ID: "d1", AgentID: master.ID, Cmd: "dismiss", Args: localchan.TextArgs(dev.Name), Flags: map[string]any{}},
			Caller:  localchan.Caller{Identified: true, AgentID: master.ID},
		})
	}
	if resp := dismiss(); resp.OK || !strings.Contains(string(resp.Data), "scheduled") {
		t.Fatalf("dismiss of a character with its task open: %+v", resp)
	}
	if c, _ := x.orc.Registry.Get(ctx, dev.ID); c.State != characters.Idle || c.StopRequestedBy == "" || !x.aliveOnTower(dev.ID) {
		t.Fatalf("with the dismissal pending the character is %s (stop requested by %q, alive on the Tower %v); want idle, asked and alive", c.State, c.StopRequestedBy, x.aliveOnTower(dev.ID))
	}
	// the character says it is done, from the Tower, through the connection
	done := forwarder{x.b.e}.Forward(ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "done1", AgentID: dev.ID, Cmd: "done", Args: localchan.TextArgs("busca implementada"), Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: dev.ID, TerminalID: dev.TerminalID},
	})
	if !done.OK {
		t.Fatalf("done from the Tower: %+v", done.Error)
	}
	// the scheduled end happens at the safe point the done made: the terminal ends on the Tower
	x.r.waitFor("the character to be stopped and its terminal gone from the Tower", func() bool {
		return x.state(t, dev.ID) == characters.Stopped && !x.aliveOnTower(dev.ID)
	})
	if c, _ := x.orc.Registry.Get(ctx, dev.ID); c.TaskOpen {
		t.Error("the task is still open after the done")
	}
}
