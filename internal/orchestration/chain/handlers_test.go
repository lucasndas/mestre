package chain_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

type cliResult struct {
	OK   bool
	Exit int
	Code string
	Msg  string
	Data map[string]any
}

// cli runs a command of the engine as the character, the way the forwarder of the
// Citadel gets it.
func (w *world) cli(by characters.Character, cmd string, flags map[string]any, args ...string) cliResult {
	w.t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	resp := w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: by.ID, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	out := cliResult{OK: resp.OK, Exit: resp.ExitCode()}
	if resp.Error != nil {
		out.Code, out.Msg = resp.Error.Code, resp.Error.Message
	}
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &out.Data)
	}
	return out
}

// CA-MM-06 (by the command): the table master decides with `approval decide`
// what is at its level and nothing else, and the answers say why.
func TestApprovalDecideByTheCommandDecidesWhatIsAtTheLevelAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, _, stranger := w.otherTable()
	for _, id := range []int64{w.hookReq(t, dev, "npm install lodash", classes.RiskLow), w.hookReq(t, dev, "rm -r build", classes.RiskMedium), w.hookReq(t, dev, "docker compose up", classes.RiskMedium)} {
		got := w.cli(w.master, "approval decide", map[string]any{"reason": "serve à tarefa"}, itoa(id), "approve")
		if !got.OK || got.Data["state"] != "approved" {
			t.Errorf("#%d: %+v, want approved", id, got)
		}
	}
	global := w.shell(dev, "npm install -g x")
	foreign := w.shell(stranger, "npm install lodash")
	own := w.shell(w.master, "npm install lodash")
	for name, id := range map[string]int64{"the Grand Master's": global, "another table's": foreign, "its own": own} {
		got := w.cli(w.master, "approval decide", map[string]any{"reason": "tento"}, itoa(id), "approve")
		if got.OK || got.Code != "not_at_level" || got.Exit != 3 {
			t.Errorf("%s: %+v, want exit 3 not_at_level", name, got)
		}
		if r := w.request(id); r.State != "open" {
			t.Errorf("%s: the request changed to %s", name, r.State)
		}
	}
	// the reason of an agent is asked, the answer must be one of the catalogue and the
	// request must be there
	id := w.hookReq(t, dev, "npm install left-pad", classes.RiskLow)
	if got := w.cli(w.master, "approval decide", nil, itoa(id), "approve"); got.OK || got.Code != "usage" {
		t.Errorf("no reason: %+v, want usage", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "x"}, itoa(id), "maybe"); got.OK || got.Code != "usage" {
		t.Errorf("an answer that is not one: %+v, want usage", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "x"}, "9999", "approve"); got.OK || got.Code != "not_found" || got.Exit != 4 {
		t.Errorf("a request that does not exist: %+v, want exit 4 not_found", got)
	}
	w.cli(w.master, "approval decide", map[string]any{"reason": "ok"}, itoa(id), "approve")
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "ok"}, itoa(id), "approve"); got.OK || got.Code != "already_decided" || got.Exit != 7 {
		t.Errorf("the same request again: %+v, want exit 7 already_decided", got)
	}
}

// CA-CLI-GM-11
func TestApprovalDecideApprovesAPatternForAWhileAndRefusesTheOnesTooBroadOrTooLong(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.hookReq(t, dev, "npm install lodash", classes.RiskLow)
	got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "2h", "pattern": "npm install *"}, itoa(id), "approve-pattern")
	if !got.OK || got.Data["applied"] != "temporary_rule" {
		t.Fatalf("approve-pattern for 2h: %+v, want a temporary rule", got)
	}
	id2 := w.hookReq(t, dev, "npm install left-pad", classes.RiskLow)
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "pattern": "npm install *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "usage" {
		t.Errorf("a pattern with no duration: %+v, want usage", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "1h", "until-quest": true, "pattern": "npm install *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "usage" {
		t.Errorf("both durations: %+v, want usage", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "-1h", "pattern": "npm install *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "usage" {
		t.Errorf("a negative duration: %+v, want usage", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "48h", "pattern": "npm install *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "too_long" || got.Exit != 2 {
		t.Errorf("48h: %+v, want exit 2 too_long", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "1h", "pattern": "sh *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "pattern_too_broad" || got.Exit != 2 {
		t.Errorf("sh *: %+v, want exit 2 pattern_too_broad", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"reason": "deps", "for": "soon", "pattern": "npm install *"}, itoa(id2), "approve-pattern"); got.OK || got.Code != "usage" {
		t.Errorf("a duration that is not one: %+v, want usage", got)
	}
	always := w.cli(w.master, "approval decide", map[string]any{"reason": "sempre"}, itoa(id2), "always")
	if !always.OK || always.Data["suggested"] != true {
		t.Errorf("always: %+v, want suggested", always)
	}
}

// CA-CLI-GM-16 and `approval list`
func TestApprovalReplyAnswersTheQuestionOfALevelOnlyForWhoAsked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	other := w.recruit("QA")
	id := w.hookReq(t, dev, "npm install lodash", classes.RiskLow)
	if got := w.cli(dev, "approval reply", nil, itoa(id), "oi"); got.OK || got.Code != "nothing_to_answer" || got.Exit != 7 {
		t.Errorf("nobody asked: %+v, want exit 7 nothing_to_answer", got)
	}
	if got := w.cli(w.master, "approval decide", map[string]any{"text": "para quê?"}, itoa(id), "ask-back"); !got.OK || got.Data["applied"] != "asked_back" {
		t.Fatalf("ask-back: %+v", got)
	}
	if got := w.cli(other, "approval reply", nil, itoa(id), "eu respondo"); got.OK || got.Code != "not_requester" || got.Exit != 3 {
		t.Errorf("another character: %+v, want exit 3 not_requester", got)
	}
	if got := w.cli(dev, "approval reply", nil, itoa(id), "  "); got.OK || got.Code != "usage" {
		t.Errorf("an empty text: %+v, want usage", got)
	}
	if got := w.cli(dev, "approval reply", nil, itoa(id), "é só o teste local"); !got.OK {
		t.Fatalf("reply: %+v", got)
	}
	if got := w.cli(dev, "approval reply", nil, "9999", "x"); got.OK || got.Code != "not_found" || got.Exit != 4 {
		t.Errorf("a request that does not exist: %+v", got)
	}

	// the list: the pending ones at the level of who asks, then the decided
	mine := w.cli(w.master, "approval list", map[string]any{"mine": true})
	items, _ := mine.Data["items"].([]any)
	if !mine.OK || len(items) != 1 || int64(items[0].(map[string]any)["id"].(float64)) != id {
		t.Fatalf("list --mine = %+v, want only #%d", mine, id)
	}
	w.cli(w.master, "approval decide", map[string]any{"reason": "ok"}, itoa(id), "approve")
	decided := w.cli(w.master, "approval list", map[string]any{"state": "decided"})
	list, _ := decided.Data["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["decided_by"] != w.master.ID {
		t.Fatalf("list --state decided = %+v, want the one the table master decided", decided.Data)
	}
	if pending := w.cli(w.master, "approval list", nil); strings.Contains(pending.Data["text"].(string), "#"+itoa(id)) {
		t.Errorf("the pending list shows a decided request: %v", pending.Data["text"])
	}
	if got := w.cli(w.master, "approval list", map[string]any{"state": "weird"}); got.OK || got.Code != "usage" {
		t.Errorf("a state that is not one: %+v", got)
	}
}

func TestTheTableMasterListsOnlyTheRequestsOfItsOwnTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, _, stranger := w.otherTable()
	mine := w.hookReq(t, dev, "npm install lodash", classes.RiskLow)
	foreign := w.shell(stranger, "npm install lodash")
	got := w.cli(w.master, "approval list", map[string]any{"state": "all"})
	text, _ := got.Data["text"].(string)
	if !got.OK || !strings.Contains(text, "#"+itoa(mine)) || strings.Contains(text, "#"+itoa(foreign)) {
		t.Fatalf("the table master lists %q, want its own #%d and not #%d", text, mine, foreign)
	}
	if r := w.cli(w.master, "approval list", map[string]any{"table": "99-outra"}); r.OK || r.Code != "level_denied" {
		t.Errorf("another table: %+v, want level_denied", r)
	}
	gmList := w.cli(w.gm, "approval list", map[string]any{"state": "all"})
	if all, _ := gmList.Data["text"].(string); !strings.Contains(all, "#"+itoa(foreign)) {
		t.Errorf("the Grand Master lists %q, want the requests of every table", all)
	}
}

// CA-BLOQ-02 (`approval ask`) and CA-GM-07: the command takes the request to the
// users and answers 9 with the number; the motive outside the vocabulary is a
// usage error with the ten values.
func TestApprovalAskTakesTheRequestToTheUsersAndRefusesAMotiveOutsideTheVocabulary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install -g x")
	flags := map[string]any{"action": itoa(id), "summary": "global", "details": "npm install -g x", "recommend": "approve", "reason": "sistema"}
	bad := map[string]any{"motive": "urgente"}
	for k, v := range flags {
		bad[k] = v
	}
	if got := w.cli(w.gm, "approval ask", bad); got.OK || got.Code != "usage" || !strings.Contains(got.Msg, "decisao_humana") {
		t.Fatalf("a motive outside the vocabulary: %+v, want usage with the values", got)
	}
	if w.level(id) != chain.LevelGrandMaster {
		t.Fatal("a refused ask moved the request")
	}
	flags["motive"] = "decisao_humana"
	got := w.cli(w.gm, "approval ask", flags)
	if got.OK || got.Exit != 9 || got.Code != "pending" {
		t.Fatalf("ask = %+v, want exit 9 pending", got)
	}
	if w.level(id) != chain.LevelPeople || len(w.users.askedFor(id)) != 1 {
		t.Fatal("the request did not reach the people")
	}
	if again := w.cli(w.gm, "approval ask", flags); again.OK || again.Code != "already_asked" || again.Exit != 7 {
		t.Fatalf("the second ask = %+v, want exit 7 already_asked", again)
	}
	if got := w.cli(w.master, "approval ask", flags); got.OK || got.Code != "level_denied" {
		t.Fatalf("a table master = %+v, want level_denied", got)
	}
	for _, drop := range []string{"summary", "details", "reason", "recommend"} {
		less := map[string]any{}
		for k, v := range flags {
			if k != drop {
				less[k] = v
			}
		}
		if got := w.cli(w.gm, "approval ask", less); got.OK || got.Code != "usage" {
			t.Errorf("without --%s: %+v, want usage", drop, got)
		}
	}
	if got := w.cli(w.gm, "approval ask", map[string]any{"action": "9999", "summary": "x", "details": "x", "recommend": "deny", "reason": "x"}); got.OK || got.Code != "not_found" || got.Exit != 4 {
		t.Fatalf("a request that does not exist: %+v", got)
	}
}

// While the Mestre is locked no approval is applied (docs/aprovacoes/fluxo.md,
// /travar): `approval decide` is refused by the gate, the request stays open and
// no exception and no temporary rule is made; after the unlock it decides.
// `approval reply` and `approval list` apply nothing and go on.
func TestWhileTheMestreIsLockedApprovalDecideAppliesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	var locked atomic.Bool
	gate := cli.NewGate(cli.GateConfig{
		Profiles: cli.ProfilesFunc(func(_ context.Context, id string) cli.Profile {
			switch id {
			case w.master.ID:
				return cli.Profile{Role: cli.RoleTableMaster}
			case w.gm.ID:
				return cli.Profile{Role: cli.RoleGrandMaster}
			}
			return cli.Profile{Role: cli.RoleCharacter}
		}),
		Next: w.fwd, Locked: func(context.Context) bool { return locked.Load() },
	})
	call := func(by characters.Character, cmd string, flags map[string]any, args ...string) localchan.Response {
		if flags == nil {
			flags = map[string]any{}
		}
		return gate.Forward(w.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: by.ID, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
			Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	}
	id := w.hookReq(t, dev, "npm install lodash", classes.RiskLow)
	locked.Store(true)
	for _, answer := range []string{"approve", "approve-pattern", "approve-for"} {
		got := call(w.master, "approval decide", map[string]any{"reason": "ok", "for": "1h", "pattern": "npm install *"}, itoa(id), answer)
		if got.OK || got.Error == nil || got.Error.Code != "locked" || got.ExitCode() != 3 {
			t.Fatalf("%s while locked: %+v, want exit 3 locked", answer, got.Error)
		}
	}
	if r := w.request(id); r.State != chain.Open || r.Decision != nil {
		t.Fatalf("the request changed while locked: %+v", r)
	}
	if len(w.temp.rules) != 0 || len(w.cls.decrees[dev.ID]) != 0 || len(w.grants.decrees) != 0 {
		t.Fatalf("something was granted while locked: rules %v, exceptions %v, decrees %v", w.temp.rules, w.cls.decrees[dev.ID], w.grants.decrees)
	}
	if got := call(w.gm, "approval autonomy", map[string]any{"table": w.table.ID, "reason": "voltar"}, "resume"); got.OK || got.Error == nil || got.Error.Code != "locked" {
		t.Errorf("resume of the autonomy while locked: %+v, want locked", got.Error)
	}
	if got := call(w.master, "approval list", nil); !got.OK {
		t.Errorf("list while locked: %+v, want it to go on", got.Error)
	}
	locked.Store(false)
	if got := call(w.master, "approval decide", map[string]any{"reason": "ok"}, itoa(id), "approve"); !got.OK {
		t.Fatalf("approve after the unlock: %+v", got.Error)
	}
}

// The command is in the definition of the CLI and goes through the gate: a text
// that looks like a secret is refused before the engine sees it, and the level is
// the Grand Master's.
func TestApprovalAskGoesThroughTheGateWhichRefusesASecretAndALowerLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install -g x")
	gate := cli.NewGate(cli.GateConfig{
		Profiles: cli.ProfilesFunc(func(_ context.Context, who string) cli.Profile {
			if who == w.gm.ID {
				return cli.Profile{Role: cli.RoleGrandMaster}
			}
			return cli.Profile{Role: cli.RoleTableMaster}
		}),
		Next: w.fwd,
	})
	call := func(by characters.Character, flags map[string]any) localchan.Response {
		return gate.Forward(w.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: by.ID, Cmd: "approval ask", Flags: flags},
			Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	}
	flags := func(summary string) map[string]any {
		return map[string]any{"action": itoa(id), "summary": summary, "details": "npm install -g x", "recommend": "approve", "reason": "sistema"}
	}
	fakeToken := "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz"
	if got := call(w.gm, flags("a chave é "+fakeToken)); got.OK || got.Error == nil || got.Error.Code != "secret_detected" {
		t.Fatalf("a secret in the summary: %+v, want secret_detected", got.Error)
	}
	if got := call(w.master, flags("x")); got.OK || got.Error == nil || got.Error.Code != "level_denied" {
		t.Fatalf("a table master: %+v, want level_denied", got.Error)
	}
	if w.level(id) != chain.LevelGrandMaster {
		t.Fatal("a refused ask moved the request")
	}
	if got := call(w.gm, flags("instalar global")); got.OK || got.ExitCode() != 9 {
		t.Fatalf("ask through the gate: %+v, want exit 9", got)
	}
	if w.level(id) != chain.LevelPeople {
		t.Fatal("the request did not reach the people")
	}
}

// CA-GM-07 (`approval ask --action "<text>"`): an action that is not a request yet
// becomes a question of the type acao_do_grao_mestre, straight to the people.
func TestApprovalAskWithATextOfActionOpensAQuestionForThePeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	flags := map[string]any{"action": "apagar a branch epico/7-teste", "summary": "limpeza", "details": "já mesclada", "recommend": "approve", "reason": "destrutivo"}
	got := w.cli(w.gm, "approval ask", flags)
	if got.OK || got.Exit != 9 || got.Code != "pending" || got.Data["request"] == nil {
		t.Fatalf("ask = %+v, want exit 9 with the request", got)
	}
	id := int64(got.Data["request"].(float64))
	if r := w.request(id); r.Type != "acao_do_grao_mestre" || r.Level.Kind != chain.LevelPeople {
		t.Fatalf("request = %+v", r)
	}
	if len(w.users.askedFor(id)) != 1 {
		t.Fatal("the people were not asked")
	}
	// a table master cannot ask, and a secret in the action is refused by the gate (see the Spec)
	if got := w.cli(w.master, "approval ask", flags); got.OK || got.Code != "level_denied" {
		t.Fatalf("a table master: %+v", got)
	}
}
