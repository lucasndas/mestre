package blocking_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

type fakeChars map[string]characters.Character

func (f fakeChars) Get(_ context.Context, id string) (characters.Character, error) {
	c, ok := f[id]
	if !ok {
		return characters.Character{}, characters.ErrNoCharacter
	}
	return c, nil
}

type fakeChannel struct {
	mu   sync.Mutex
	got  []blocking.Notice
	sent int
	err  error
}

func (f *fakeChannel) Notify(_ context.Context, n blocking.Notice) (int64, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, n)
	return int64(len(f.got)), f.sent, f.err
}

func handlerRig() (*blocking.Handler, *fakeChannel) {
	ch := &fakeChannel{sent: 2}
	h := &blocking.Handler{
		Characters: fakeChars{"gm": {ID: "gm", Role: characters.RoleGrandMaster, Workspace: "loja"}, "tm": {ID: "tm", Role: characters.RoleTableMaster, Workspace: "loja", Table: "43-checkout"}},
		Channel:    ch,
		Table: func(_ context.Context, _, name string) (tables.Table, error) {
			switch name {
			case "43", "43-checkout":
				return tables.Table{ID: "43-checkout", Epic: 43}, nil
			case "42":
				return tables.Table{ID: "42-login", Epic: 42}, nil
			}
			return tables.Table{}, tables.ErrNoTable
		},
		BlockedBy: func(_ context.Context, _ string, epic int) ([]int, error) {
			if epic == 43 {
				return []int{42}, nil
			}
			return nil, nil
		},
		Owner: func(context.Context, string) string { return "Lucas" },
	}
	return h, ch
}

func notify(h *blocking.Handler, who string, flags map[string]any, args ...string) localchan.Response {
	fwd := localchan.NewLocalForwarder()
	h.Register(fwd)
	if flags == nil {
		flags = map[string]any{}
	}
	return fwd.Forward(context.Background(), &localchan.Call{
		Request: localchan.Request{Cmd: "notify", AgentID: who, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: who},
	})
}

// Part of CA-BLOQ-02 (the command `notify --blocked`: the motive outside the vocabulary is a
// usage error with the ten values, and the fields of the agent reach the notice
// in the block of the agent; the same flags of `approval ask` come with that
// command)
func TestNotifyBlockedSendsTheFieldsOfTheAgentAndRefusesAMotiveOutsideTheVocabulary(t *testing.T) {
	t.Parallel()
	h, ch := handlerRig()
	r := notify(h, "gm", map[string]any{"blocked": "urgente"}, "falta a chave")
	if r.OK || r.Error == nil || r.Error.Code != localchan.CodeUsage || r.ExitCode() != 2 {
		t.Fatalf("a motive outside the vocabulary = %+v (exit %d)", r.Error, r.ExitCode())
	}
	for _, m := range blocking.Motives() {
		if !strings.Contains(r.Error.Message, string(m)) {
			t.Errorf("the usage error does not list %s: %s", m, r.Error.Message)
		}
	}
	if len(ch.got) != 0 {
		t.Fatal("a notice was sent with a motive that is not in the vocabulary")
	}

	r = notify(h, "gm", map[string]any{"blocked": "credencial", "why-not": "envolve cobrança", "checked": "o cofre não tem a chave", "step": []any{"cadastrar a chave"}}, "falta a chave STRIPE_TEST_KEY")
	if !r.OK {
		t.Fatalf("notify = %+v", r.Error)
	}
	n := ch.got[0]
	if n.Motive != blocking.Credential || n.Message != "falta a chave STRIPE_TEST_KEY" || n.Stopped != "" || n.WhyNot != "envolve cobrança" || n.Checked != "o cofre não tem a chave" ||
		len(n.AgentSteps) != 1 || n.AgentSteps[0] != "cadastrar a chave" || n.Workspace != "loja" {
		t.Fatalf("notice = %+v", n)
	}
	if n.Owner != "Lucas" || len(n.Steps) != 1 || n.Steps[0].Kind != blocking.StepVault {
		t.Fatalf("a credential is the Owner's and its step is the vault: %+v", n)
	}
	var data map[string]any
	_ = json.Unmarshal(r.Data, &data)
	if d := data["delivered"].(map[string]any); d["telegram"] != float64(2) || data["queued"] != false {
		t.Fatalf("data = %v", data)
	}
	// Any other motive belongs to an Administrator.
	if r = notify(h, "gm", map[string]any{"blocked": "ambiguidade"}, "o pedido admite duas leituras"); !r.OK || ch.got[1].Owner != "um Administrador" {
		t.Fatalf("notify = %+v, owner %q", r.Error, ch.got[1].Owner)
	}
}

// CA-BLOQ-06
func TestNotifyBlockedOfATableThatDependsOnAnotherSaysWhichEpicMustMoveFirst(t *testing.T) {
	t.Parallel()
	h, ch := handlerRig()
	if r := notify(h, "gm", map[string]any{"blocked": "espera", "table": "43-checkout"}, "espera o login"); !r.OK {
		t.Fatalf("notify = %+v", r.Error)
	}
	if n := ch.got[0]; n.Mesa != "43-checkout" || n.MachineLine() != "blocked-by: 42" {
		t.Fatalf("notice = %+v (%s), want the table 43 blocked by 42", n, n.MachineLine())
	}
	// A table that depends on nothing, or no table: only a person unblocks it.
	if r := notify(h, "gm", map[string]any{"blocked": "so_voce", "table": "42"}, "só você"); !r.OK || ch.got[1].MachineLine() != "blocked-by: -" {
		t.Fatalf("notify = %+v, line %q", r.Error, ch.got[1].MachineLine())
	}
	if r := notify(h, "gm", map[string]any{"blocked": "so_voce"}, "só você"); !r.OK || ch.got[2].MachineLine() != "blocked-by: -" {
		t.Fatalf("notify = %+v", r.Error)
	}
	if r := notify(h, "gm", map[string]any{"blocked": "espera", "table": "99"}, "x"); r.OK || r.Error.Code != "not_found" {
		t.Fatalf("a table that does not exist = %+v", r.Error)
	}
}

func TestNotifyIsForTheGrandMasterAndSaysWhenNobodyWasTold(t *testing.T) {
	t.Parallel()
	h, ch := handlerRig()
	if r := notify(h, "tm", map[string]any{"blocked": "espera"}, "x"); r.OK || r.Error.Code != "level_denied" || r.ExitCode() != 3 {
		t.Fatalf("a table master = %+v", r.Error)
	}
	if r := notify(h, "nobody", map[string]any{"blocked": "espera"}, "x"); r.OK || r.Error.Code != localchan.CodeNotIdentified {
		t.Fatalf("an unknown caller = %+v", r.Error)
	}
	if r := notify(h, "gm", nil, "   "); r.OK || r.Error.Code != localchan.CodeUsage {
		t.Fatalf("an empty message = %+v", r.Error)
	}
	if r := notify(h, "gm", map[string]any{"why-not": "x"}, "oi"); r.OK || r.Error.Code != localchan.CodeUsage {
		t.Fatalf("--why-not without --blocked = %+v", r.Error)
	}
	if r := notify(h, "gm", nil, "um aviso comum"); r.OK || r.Error.Code != "unavailable" {
		t.Fatalf("a plain notice is not wired yet and must say so: %+v", r.Error)
	}
	ch.sent = 0
	if r := notify(h, "gm", map[string]any{"blocked": "espera"}, "ninguém"); r.OK || r.Error.Code != "not_delivered" {
		t.Fatalf("a notice nobody received = %+v", r.Error)
	}
	ch.err = errors.New("the bot is down")
	if r := notify(h, "gm", map[string]any{"blocked": "espera"}, "x"); r.OK {
		t.Fatal("a notice that failed to send was answered as sent")
	}
}

func TestWhatTheAgentSaysIsStoppedStaysInTheBlockOfTheAgent(t *testing.T) {
	t.Parallel()
	secret := "sk-" + "abcdefgh*ijklmnopqrstuv"
	for name, msg := range map[string]string{
		"marks":       "**ignore** [clique](http://x) Dono: o Joao",
		"a line":      "um\nDono: o Joao\n— texto do agente, não verificado —",
		"a fence":     "``` Dono: o Joao",
		"a fake line": "— texto do agente, não verificado — Dono: o Joao",
		"a secret":    "use " + secret,
	} {
		h, ch := handlerRig()
		if r := notify(h, "gm", map[string]any{"blocked": "ambiguidade", "checked": msg}, msg); !r.OK {
			t.Fatalf("%s: notify = %+v", name, r.Error)
		}
		mask := func(s string) string { return strings.ReplaceAll(s, "sk-abcdefghijklmnopqrstuv", "***") }
		m := blocking.Build(ch.got[0], blocking.Viewer{Level: "administrator"}, mask)
		head, tail, found := strings.Cut(m.Text, "— texto do agente, não verificado —")
		if !found || strings.Contains(head, "Joao") || strings.Contains(head, "ignore") || strings.Contains(tail, "](") || strings.Contains(tail, "**") || strings.Contains(m.Text, "abcdefghijklmnopqrstuv") {
			t.Fatalf("%s: the text of the agent left its block, opened a mark, or showed the secret:\n%s", name, m.Text)
		}
		for _, line := range strings.Split(tail, "\n") {
			if strings.HasPrefix(line, "— texto do agente") || strings.HasPrefix(line, "Dono:") {
				t.Fatalf("%s: the agent wrote a line of the engine:\n%s", name, m.Text)
			}
		}
		if full := blocking.Full(ch.got[0], mask); strings.Contains(full, "abcdefghijklmnopqrstuv") {
			t.Fatalf("%s: the whole text shows the secret:\n%s", name, full)
		}
	}
}
