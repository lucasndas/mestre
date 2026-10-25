package telegram_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/users"
)

// supChars knows one Grand Master and no table master.
type supChars struct {
	chain.Characters
	gm characters.Character
}

func (s supChars) Get(_ context.Context, id string) (characters.Character, error) {
	if id == s.gm.ID {
		return s.gm, nil
	}
	return characters.Character{}, context.Canceled
}

func (s supChars) TableMaster(context.Context, string, string) (characters.Character, bool, error) {
	return characters.Character{}, false, nil
}

func (s supChars) GrandMaster(context.Context, string) (characters.Character, bool, error) {
	return s.gm, true, nil
}

type supInbox struct {
	chain.Inbox
	mu    sync.Mutex
	items []inbox.NewItem
}

func (i *supInbox) Enqueue(_ context.Context, n inbox.NewItem) (inbox.Item, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.items = append(i.items, n)
	return inbox.Item{}, nil
}

// CA-SUPCAD-05
func TestTheGrandMasterPausesTheAutonomyByTheCommandAndAnApproverByTheButtonOfTheQueueWarning(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	w.pairOwnerTelegram()
	approverU, approver := w.user("Caio", users.Approver, "loja")
	_, observer := w.user("Edu", users.Observer, "loja")
	gm := characters.Character{ID: "gm1", Name: "Grão", Workspace: "loja", Role: characters.RoleGrandMaster}
	ib := &supInbox{}
	sup, err := chain.NewSupervisor(chain.SupervisorOptions{Store: &chain.Store{DB: w.db}, Clock: w.clk, Characters: supChars{gm: gm}, Inbox: ib,
		Policy: chain.StaticPolicy{}, QueuePeople: w.sc})
	if err != nil {
		t.Fatal(err)
	}
	w.sc.Autonomy = sup
	fwd := localchan.NewLocalForwarder()
	sup.Register(fwd)
	call := func(verb, table string, flags map[string]any) localchan.Response {
		flags["table"] = table
		return fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "approval autonomy", Args: localchan.TextArgs(verb), Flags: flags},
			Caller: localchan.Caller{Identified: true, AgentID: gm.ID}})
	}

	// the Grand Master, by the command: the table is paused and the audit says who and why
	if r := call("pause", "42-login", map[string]any{"reason": "muitas falhas"}); !r.OK {
		t.Fatalf("pause by the Grand Master: %+v", r.Error)
	}
	log, err := sup.PauseAudit(w.ctx, "loja", "42-login")
	if err != nil || len(log) != 1 || log[0].Action != "pause" || log[0].By != gm.ID || log[0].Reason != "muitas falhas" {
		t.Fatalf("audit = %+v, %v", log, err)
	}
	if paused, _ := sup.AutonomyPaused(w.ctx, "loja", "42-login"); !paused {
		t.Fatal("the table is not paused")
	}

	// an Approver, by the button of the queue warning of another table
	if err := w.sc.QueueAccumulating(w.ctx, chain.QueueNotice{Workspace: "loja", Table: "43-busca", Text: "Fila acumulando: Mestre da mesa 43-busca tem 11 pedidos."}); err != nil {
		t.Fatal(err)
	}
	w.drain()
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == observer.ID {
			t.Errorf("the warning reached an Observer: %q", s.Text)
		}
	}
	msg, keys := w.buttons(approver)
	if keys["Pausar autonomia"] == "" {
		t.Fatalf("the Approver has the buttons %v", keys)
	}
	w.click(approver, msg, keys["Pausar autonomia"])
	if paused, _ := sup.AutonomyPaused(w.ctx, "loja", "43-busca"); !paused {
		t.Fatal("the button did not pause the autonomy of the table")
	}
	log, _ = sup.PauseAudit(w.ctx, "loja", "43-busca")
	if len(log) != 1 || log[0].Action != "pause" || log[0].By != "user:"+approverU.ID || !strings.Contains(log[0].Reason, "fila") {
		t.Fatalf("audit of the button = %+v", log)
	}
	// the Grand Master can not resume the pause of a person
	if r := call("resume", "43-busca", map[string]any{"reason": "voltar"}); r.OK || r.Error.Code != "level_denied" {
		t.Errorf("resume by the Grand Master of the pause of a person = %+v", r)
	}
	if paused, _ := sup.AutonomyPaused(w.ctx, "loja", "43-busca"); !paused {
		t.Error("the refused resume changed the pause")
	}
	// an Approver of another workspace has no button for this one, and an Observer has no message
	if _, keys := w.buttons(observer); len(keys) != 0 {
		t.Errorf("an Observer was given %v", keys)
	}
}

// CA-USR-10
//
// The resume clause of the criterion: an Approver taps
// (Pausar autonomia) and the pause is of a person; resuming it as an Approver
// is accepted by the engine, and an Observer is refused.
func TestAnApproverResumesThePauseOfAPersonAndTheEngineAccepts(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	w.pairOwnerTelegram()
	approverU, approver := w.user("Caio", users.Approver, "loja")
	gm := characters.Character{ID: "gm1", Name: "Grão", Workspace: "loja", Role: characters.RoleGrandMaster}
	sup, err := chain.NewSupervisor(chain.SupervisorOptions{Store: &chain.Store{DB: w.db}, Clock: w.clk, Characters: supChars{gm: gm}, Inbox: &supInbox{},
		Policy: chain.StaticPolicy{}, QueuePeople: w.sc})
	if err != nil {
		t.Fatal(err)
	}
	w.sc.Autonomy = sup
	if err := w.sc.QueueAccumulating(w.ctx, chain.QueueNotice{Workspace: "loja", Table: "43-busca", Text: "Fila acumulando: Mestre da mesa 43-busca tem 11 pedidos."}); err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(approver)
	w.click(approver, msg, keys["Pausar autonomia"])
	if p, ok, _ := sup.PauseOf(w.ctx, "loja", "43-busca"); !ok || p.Kind != "person" || p.By != "user:"+approverU.ID {
		t.Fatalf("pause = %+v, %v; want the pause of the Approver", p, ok)
	}
	// an Observer is refused; the pause stays
	if _, err := sup.ResumeAutonomy(w.ctx, chain.PauseRequest{Workspace: "loja", Table: "43-busca", Reason: "x", By: chain.Actor{User: "u-obs", UserLevel: characters.LevelObserver}}); err == nil {
		t.Error("an Observer resumed the pause of a person")
	}
	// the Approver resumes: the engine accepts
	changed, err := sup.ResumeAutonomy(w.ctx, chain.PauseRequest{Workspace: "loja", Table: "43-busca", Reason: "voltar", By: chain.Actor{User: approverU.ID, UserLevel: characters.LevelApprover}})
	if err != nil || !changed {
		t.Fatalf("resume by an Approver = %v, %v; want accepted", changed, err)
	}
	if paused, _ := sup.AutonomyPaused(w.ctx, "loja", "43-busca"); paused {
		t.Error("the table is still paused after the Approver resumed it")
	}
}
