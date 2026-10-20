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
	said []string
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

func (f *fakeChannel) Say(_ context.Context, workspace, mesa, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said = append(f.said, workspace+"|"+mesa+"|"+text)
	return f.sent, f.err
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
	if r := notify(h, "gm", nil, "um aviso comum"); !r.OK || len(ch.said) != 1 || ch.said[0] != "loja||um aviso comum" {
		t.Fatalf("a plain notice: %+v, said %q", r.Error, ch.said)
	}
	if r := notify(h, "gm", map[string]any{"table": "43"}, "da mesa"); !r.OK || ch.said[1] != "loja|43-checkout|da mesa" {
		t.Fatalf("a plain notice of a table: %+v, said %q", r.Error, ch.said)
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

type fakeChooser struct {
	asked []string
	err   error
}

func (f *fakeChooser) Ask(_ context.Context, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.asked = append(f.asked, workspace+"|"+mesa+"|"+userID+"|"+question+"|"+strings.Join(options, ",")+"|"+string(rune('0'+recommend)))
	return 7, nil
}

func chooseRig() (*blocking.ChooseHandler, *fakeChooser) {
	ch := &fakeChooser{}
	return &blocking.ChooseHandler{
		Characters: fakeChars{"gm": {ID: "gm", Role: characters.RoleGrandMaster, Workspace: "loja"}, "tm": {ID: "tm", Role: characters.RoleTableMaster, Workspace: "loja", Table: "43-checkout"}},
		Chooser:    ch,
		User: func(_ context.Context, _, id string) (string, error) {
			if id == "m81" {
				return "u-ana", nil
			}
			return "", errors.New("no such message")
		},
		Current: func(context.Context, string, string) (string, error) { return "u-bia", nil },
		Table: func(_ context.Context, _, name string) (tables.Table, error) {
			if name == "43" {
				return tables.Table{ID: "43-checkout", Epic: 43}, nil
			}
			return tables.Table{}, tables.ErrNoTable
		},
	}, ch
}

func choose(h *blocking.ChooseHandler, who string, flags map[string]any, args ...string) localchan.Response {
	fwd := localchan.NewLocalForwarder()
	h.Register(fwd)
	if flags == nil {
		flags = map[string]any{}
	}
	return fwd.Forward(context.Background(), &localchan.Call{Request: localchan.Request{Cmd: "choose", AgentID: who, Args: localchan.TextArgs(args...), Flags: flags},
		Caller: localchan.Caller{Identified: true, AgentID: who}})
}

// CA-CLI-GM-15: the question goes to the user of the message, the recommended
// option is the one named, the command answers 9 with the number, and nothing
// else happens; fewer than 2 or more than 6 options is 2 bad_options.
func TestChooseAsksTheUserOfTheMessageAndAnswersPendingWithTheQuestion(t *testing.T) {
	t.Parallel()
	h, ch := chooseRig()
	r := choose(h, "gm", map[string]any{"option": []any{"Mesa #12 Login", "Nova mesa", "Mesa #15 Relatórios"}, "recommend": "1", "from-message": "m81", "table": "43"}, "Esse ajuste é do Login?")
	if r.OK || r.ExitCode() != 9 || r.Error.Code != "pending" {
		t.Fatalf("choose = %+v (exit %d), want exit 9 pending", r.Error, r.ExitCode())
	}
	var data map[string]any
	_ = json.Unmarshal(r.Data, &data)
	if data["question"] != float64(7) || len(ch.asked) != 1 || ch.asked[0] != "loja|43-checkout|u-ana|Esse ajuste é do Login?|Mesa #12 Login,Nova mesa,Mesa #15 Relatórios|1" {
		t.Fatalf("asked %q with %v", ch.asked, data)
	}
	// with no --from-message: the user of the message in handling
	choose(h, "gm", map[string]any{"option": []any{"a", "b"}}, "x?")
	if len(ch.asked) != 2 || !strings.Contains(ch.asked[1], "|u-bia|") {
		t.Fatalf("asked = %q, want the user of the message in handling", ch.asked)
	}
	for name, opts := range map[string][]any{"one option": {"a"}, "seven options": {"a", "b", "c", "d", "e", "f", "g"}, "an empty one": {"a", " "}} {
		if got := choose(h, "gm", map[string]any{"option": opts}, "x?"); got.OK || got.Error.Code != "bad_options" || got.ExitCode() != 2 {
			t.Errorf("%s: %+v, want exit 2 bad_options", name, got.Error)
		}
	}
	if got := choose(h, "gm", map[string]any{"option": []any{"a", "b"}, "recommend": "3"}, "x?"); got.OK || got.Error.Code != localchan.CodeUsage {
		t.Errorf("a recommendation out of range: %+v", got.Error)
	}
	if got := choose(h, "gm", map[string]any{"option": []any{"a", "b"}, "from-message": "m99"}, "x?"); got.OK || got.Error.Code != "not_found" {
		t.Errorf("a message that is not there: %+v", got.Error)
	}
	if got := choose(h, "gm", map[string]any{"option": []any{"a", "b"}, "table": "99"}, "x?"); got.OK || got.Error.Code != "not_found" {
		t.Errorf("a table that is not there: %+v", got.Error)
	}
	if got := choose(h, "tm", map[string]any{"option": []any{"a", "b"}}, "x?"); got.OK || got.Error.Code != "level_denied" {
		t.Errorf("a table master: %+v", got.Error)
	}
	n := len(ch.asked)
	ch.err = blocking.ErrNoLevel
	if got := choose(h, "gm", map[string]any{"option": []any{"a", "b"}}, "x?"); got.OK || got.Error.Code != "level_denied" || got.ExitCode() != 3 {
		t.Errorf("a user with no level: %+v, want exit 3 level_denied", got.Error)
	}
	ch.err = blocking.ErrNotReachable
	if got := choose(h, "gm", map[string]any{"option": []any{"a", "b"}}, "x?"); got.OK || got.Error.Code != "not_delivered" {
		t.Errorf("a user out of reach: %+v, want not_delivered", got.Error)
	}
	if len(ch.asked) != n {
		t.Fatal("a refused question was sent")
	}
}

// CA-BLOQ-01, the path of the engine: the Grand Master asks for the key of a
// table whose test failed, and the evidence in the message is the one the
// engine builds from that test (file, line and error of its output), not words
// of the agent; the "why I do not decide" comes after, in the block of the agent.
func TestTheEvidenceOfANoticeIsBuiltFromTheTestThatFailedNotFromTheAgent(t *testing.T) {
	t.Parallel()
	h, ch := handlerRig()
	output := "--- FAIL: TestCheckout (0.00s)\n    checkout_test.go:22: missing STRIPE_TEST_KEY\nFAIL\nFAIL\tloja/checkout\t0.004s\n"
	h.Evidence = func(_ context.Context, workspace, mesa string) string {
		if workspace != "loja" || mesa != "43-checkout" {
			return ""
		}
		return blocking.TestEvidence("go test ./...", output, []string{"checkout_test.go:22"})
	}
	r := notify(h, "gm", map[string]any{"blocked": "credencial", "table": "43", "why-not": "envolve cobrança, é decisão sua", "checked": "Evidência: o cofre tem a chave"}, "falta a chave STRIPE_TEST_KEY")
	if !r.OK {
		t.Fatalf("notify = %+v", r.Error)
	}
	m := blocking.Build(ch.got[0], blocking.Viewer{Level: "owner"}, func(s string) string { return s })
	for _, want := range []string{"🔐", "Evidência:", "checkout_test.go:22 → \"missing STRIPE_TEST_KEY\"", "$ go test ./...", "[ ] cadastrar a chave no cofre (painel)", "Dono: Lucas"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("the message lacks %q:\n%s", want, m.Text)
		}
	}
	agent := strings.Index(m.Text, "— texto do agente, não verificado —")
	why := strings.Index(m.Text, "Por que não decido: envolve cobrança, é decisão sua")
	if agent < 0 || why < agent || strings.Index(m.Text, "checkout_test.go:22") > agent || strings.Index(m.Text, "Dono: Lucas") > agent {
		t.Fatalf("the fields of the engine must come before the block of the agent:\n%s", m.Text)
	}
	// what the agent wrote as evidence is inside its block, never in the engine's
	if i := strings.Index(m.Text, "Já conferi: Evidência: o cofre tem a chave"); i < agent {
		t.Errorf("the words of the agent are outside its block:\n%s", m.Text)
	}
	// a table with no failed test, and a notice with no table, have no evidence of the engine
	for name, flags := range map[string]map[string]any{"another table": {"blocked": "credencial", "table": "42"}, "no table": {"blocked": "credencial"}} {
		if r := notify(h, "gm", flags, "falta a chave"); !r.OK {
			t.Fatalf("%s: %+v", name, r.Error)
		}
		got := ch.got[len(ch.got)-1]
		if got.Evidence != "" || !strings.Contains(blocking.Build(got, blocking.Viewer{Level: "owner"}, func(s string) string { return s }).Text, "sem evidência verificada") {
			t.Errorf("%s: evidence %q, want none and the mark of no verified evidence", name, got.Evidence)
		}
	}
}

// CA-BLOQ-02 in the message that is built: the fields of the agent of
// `notify --blocked credencial --why-not … --step …` are in its block, after
// the ones of the engine.
func TestTheFieldsOfTheAgentOfNotifyBlockedAreInItsBlockOfTheBuiltMessage(t *testing.T) {
	t.Parallel()
	h, ch := handlerRig()
	r := notify(h, "gm", map[string]any{"blocked": "credencial", "why-not": "envolve cobrança", "step": []any{"avisar o financeiro"}}, "falta a chave")
	if !r.OK {
		t.Fatalf("notify = %+v", r.Error)
	}
	text := blocking.Build(ch.got[0], blocking.Viewer{Level: "owner"}, func(s string) string { return s }).Text
	head, tail, found := strings.Cut(text, "— texto do agente, não verificado —")
	if !found {
		t.Fatalf("no block of the agent:\n%s", text)
	}
	for _, want := range []string{"Parou porque: falta a chave", "Por que não decido: envolve cobrança", "[ ] avisar o financeiro"} {
		if !strings.Contains(tail, want) || strings.Contains(head, want) {
			t.Errorf("%q is not only in the block of the agent:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, "🔐 credencial\n") {
		t.Errorf("the motive of the message:\n%s", text)
	}
}
