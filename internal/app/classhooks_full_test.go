//go:build full

package app

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-CLASSES-04: `mestre run` with the fake agent in a terminal of its mestre-pty.
// The class of the character has a hook of turn_end that runs in the background
// with `output: scroll`; the hook is a process the terminal starts as the
// character (the HookExec of the Runtime) and the test holds it. The fake agent
// ends its turn, a second message is given to it and its next turn runs, ends,
// with the hook still held (the turn did not wait for it); when the hook ends, its
// output is in the scroll of the mission.
func TestAnAsynchronousHookOfTheClassDoesNotHoldTheNextTurnAndItsOutputReachesTheScrollOfTheMission(t *testing.T) {
	t.Parallel()
	x := newChannelRigWith(t, channelOpts{chain: true, citadelTerminals: true})
	ctx := t.Context()
	const table, branch = "4-busca", "mestre/4-busca"
	citadelTree := x.seedTable(t, table, branch, 4)
	home := filepath.Join(x.root, "home")
	script := "prompts:\n" +
		"  - match: \"(?s).*PASSO-UM.*\"\n    steps:\n      - say: \"primeiro turno\"\n" +
		"  - match: \"(?s).*SEGUNDA.*\"\n    steps:\n      - say: \"segundo turno\"\n"
	if err := os.WriteFile(filepath.Join(home, "quick.yaml"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	// the hook is held by the test: the process connects here and waits for it to close the connection
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	held := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// the hook is a program of the worktree: the permissions of the class read nothing outside it
	probe, run := "hookprobe", "./hookprobe"
	if runtime.GOOS == "windows" {
		probe = "hookprobe.exe"
		run = `"` + filepath.Join(citadelTree, probe) + `"`
	}
	if err := os.Link(exe, filepath.Join(citadelTree, probe)); err != nil {
		src, rerr := os.ReadFile(exe)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if werr := os.WriteFile(filepath.Join(citadelTree, probe), src, 0o755); werr != nil {
			t.Fatal(werr)
		}
	}
	hooks := "turn_end:\n  - {name: testes, run: '" + run + " hookprobe " + ln.Addr().String() + "', mode: async, timeout: 120, output: scroll}\n"
	approveClassOn(t, x.a, "meu-app", "dev", devClass("dev", hooks)...)

	now := clock.Real{}.Now()
	master := characters.Character{ID: "m4", Name: "Mestre", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: "meu-app", Table: table, Machine: x.a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: now}
	if err := x.orc.Registry.Insert(ctx, master); err != nil {
		t.Fatal(err)
	}
	def := "name: Busca\nsteps:\n  - {id: um, sheet: Dev Claude, machine: a, instructions: \"PASSO-UM: faça\"}\n"
	v, err := x.orc.Quests.Create(ctx, quests.CreateRequest{Workspace: "meu-app", Table: table, Data: []byte(def), Designer: master.ID})
	if err != nil {
		t.Fatal(err)
	}
	q, err := x.orc.Quests.Run(ctx, v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var dev characters.Character
	x.r.waitFor("the character of the step", func() bool {
		cs, err := x.orc.Registry.AliveOf(ctx, "meu-app")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.Quest == q.ID {
				dev = c
			}
		}
		return dev.ID != ""
	})
	scroll := func() string {
		_, text, err := x.orc.Scrolls.Read(ctx, scrolls.ReadRequest{Workspace: "meu-app", Table: table, Actor: scrolls.Engine, Name: scrolls.QuestScrollName(q.Name)})
		if err != nil {
			t.Fatal(err)
		}
		return text
	}

	// the first turn ends: the hook starts in the terminal of the character and is held there
	var first net.Conn
	limit := clock.Real{}.NewTimer(20 * time.Second)
	defer limit.Stop()
	select {
	case first = <-held:
	case <-limit.C():
		t.Fatal("the hook of the turn_end did not start in the terminal of the character")
	}
	x.r.waitFor("the first turn to end", func() bool { return x.state(t, dev.ID) == characters.Idle })
	if strings.Contains(scroll(), "ok  3 testes") {
		t.Fatal("the output of the hook is in the scroll before the hook ended")
	}

	// the next turn begins and ends while the hook is still held
	before := x.lastSignal(t, dev.ID)
	it, err := x.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: dev.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: "SEGUNDA: de novo"})
	if err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the second turn to be delivered and to end", func() bool {
		cur, err := x.orc.Inbox.Store().Get(ctx, it.ID)
		return err == nil && cur.State == inbox.Delivered && x.state(t, dev.ID) == characters.Idle && x.lastSignal(t, dev.ID).After(before)
	})
	if strings.Contains(scroll(), "ok  3 testes") {
		t.Fatal("the output of the hook is in the scroll although it is still held")
	}

	// the hook ends: its output is in the scroll of the mission
	_ = first.Close()
	x.r.waitFor("the output of the hook in the scroll of the mission", func() bool {
		s := scroll()
		return strings.Contains(s, "## Hook testes — "+dev.Name) && strings.Contains(s, "ok  3 testes")
	})
}
