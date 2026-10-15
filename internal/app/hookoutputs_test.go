package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/providers"
)

// asyncExec is the process of an asynchronous hook: it starts, waits to be
// released and prints.
type asyncExec struct {
	started chan string
	release chan struct{}
}

func (e *asyncExec) Exec(ctx context.Context, agentID string, h hook.AsyncHook, _ providers.EventKind, _ []byte) (hook.HookResult, error) {
	e.started <- agentID + "/" + h.Name
	select {
	case <-e.release:
	case <-ctx.Done():
		return hook.HookResult{}, ctx.Err()
	}
	return hook.HookResult{Output: "ok  3 testes"}, nil
}

const questOneStep = `
name: Sozinha
steps:
  - {id: a, sheet: Dev Claude, instructions: "Faça"}
  - {id: b, sheet: Revisor Codex, after: [a]}
`

// CA-CLASSES-04
func TestAnAsyncTurnEndHookOfTheClassOfACharacterInAQuestWritesToTheScrollOfTheQuest(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	w.approveClass("dev", devPerms, classes.File{Path: "hooks/hooks.yaml",
		Data: []byte("turn_end:\n  - {name: testes, run: \"npm test\", mode: async, timeout: 900, output: scroll}\n")})
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(questOneStep), Designer: w.master.ID})
	if err != nil {
		t.Fatal(err)
	}
	q, err := w.orc.Quests.Run(w.ctx, v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	alive, err := w.orc.Registry.AliveOf(w.ctx, w.slug)
	if err != nil {
		t.Fatal(err)
	}
	var dev characters.Character
	for _, c := range alive {
		if c.Sheet == "Dev Claude" && c.Quest == q.ID {
			dev = c
		}
	}
	if dev.ID == "" {
		t.Fatalf("no character of the quest: %+v", alive)
	}

	// The runner of the engine, with the output of the Citadel.
	exec := &asyncExec{started: make(chan string, 1), release: make(chan struct{})}
	runner := &hook.AsyncRunner{Source: &hook.ClassHookSource{Classes: w.lib, Rules: w.cc, Characters: w.cc}, Exec: exec, Out: w.orc.HookOutputs(), Clock: w.clk}
	runner.Start(w.ctx)
	end := providers.Event{Version: providers.EventVersion, Event: providers.EventTurnEnd, AgentID: dev.ID, TableID: dev.Table, TurnID: "p1"}
	runner.OnEvent(end, hook.Answer{Verdict: "neutral"})
	// the end of the turn did not wait for the hook: it runs now, still blocked
	if got := <-exec.started; got != dev.ID+"/testes" {
		t.Fatalf("hook started = %q", got)
	}
	if text := questScroll(t, w, q); strings.Contains(text, "ok  3 testes") {
		t.Fatal("the output reached the scroll before the hook ended")
	}
	close(exec.release)
	runner.Wait()
	text := questScroll(t, w, q)
	for _, want := range []string{"## Hook testes — " + dev.Name, "ok  3 testes"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll of the quest lacks %q:\n%s", want, text)
		}
	}
}

func questScroll(t *testing.T, w *commWorld, q quests.Quest) string {
	t.Helper()
	_, text, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: scrolls.QuestScrollName(q.Name)})
	if err != nil {
		t.Fatal(err)
	}
	return text
}
