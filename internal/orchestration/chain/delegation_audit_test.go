package chain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/fakechannel"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

// The delegations of decision end to end: the answer Delegar of a person goes
// through the real flow of approval (the PIN), the real chain makes the decree
// with the real decrees, and the chain reads that decree when the Grand Master
// or the Mestre da mesa decide (docs/aprovacoes/delegacao.md).

const delegPIN = "482913"

// delegWorld is the world of the chain with a second chain over the same
// Guild whose grants, delegations and subdelegations are the real decrees,
// and the real flow of approval over it.
type delegWorld struct {
	*world
	ch    *chain.Service
	flow  *approvals.Flow
	chn   *fakechannel.Channel
	ds    *decrees.Service
	owner users.User
}

type delegLateChain struct{ w *delegWorld }

func (c delegLateChain) Get(ctx context.Context, id int64) (chain.Request, error) {
	return c.w.ch.Get(ctx, id)
}

func (c delegLateChain) DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	return c.w.ch.DecideByPerson(ctx, d)
}

func (c delegLateChain) Invalidate(ctx context.Context, id int64, why string) error {
	return c.w.ch.Invalidate(ctx, id, why)
}

// delegTarget is the live target of every request of these tests: the same
// the request was bound to, so the flow verifies it.
var delegTarget = json.RawMessage(`{"repo":"lucas/Meu_App","path":"pergaminhos/login.md","revision":"r1"}`)

type delegTargets struct{}

func (delegTargets) Current(context.Context, chain.Request) (json.RawMessage, error) {
	return delegTarget, nil
}

func newDelegWorld(t *testing.T) *delegWorld {
	t.Helper()
	base := newWorld(t, worldOpts{flow: true})
	w := &delegWorld{world: base, owner: base.owner, chn: fakechannel.New()}
	db := base.ch.Store().DB
	if err := base.usvc.SetPIN(base.ctx, w.owner.ID, delegPIN); err != nil {
		t.Fatal(err)
	}
	var err error
	w.ds, err = decrees.New(decrees.Options{DB: db, Clock: base.clk, PINs: base.usvc, Requests: delegLateChain{w}})
	if err != nil {
		t.Fatal(err)
	}
	w.flow, err = approvals.New(approvals.Options{DB: db, Clock: base.clk, Chain: delegLateChain{w}, Users: base.usvc, PINs: base.usvc, Messenger: w.chn,
		Targets: delegTargets{}, Locked: func(context.Context) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	w.ch, err = chain.NewService(chain.Options{Store: base.ch.Store(), Clock: base.clk, Characters: chain.CharactersFrom(base.orc.Characters, base.orc.Registry, base.clk),
		Inbox: base.orc.Inbox, Classes: base.cls, Policy: base.policy, Users: w.flow, Grants: w.ds, Delegations: w.ds, SubDelegations: w.ds, Supervision: base.sup})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// open opens a request of a type alto in a table, bound to its target.
func (w *delegWorld) open(typ classes.RequestType, table, requester string) int64 {
	w.t.Helper()
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Type: typ, Workspace: w.slug, Table: table, Requester: requester, About: "apagar o pergaminho login.md",
		Risk: classes.RiskResult{Risk: classes.RiskHigh, TableVersion: classes.RiskTableVersion, Rule: string(typ)}, Target: delegTarget})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *delegWorld) at(id int64) chain.Level {
	w.t.Helper()
	r, err := w.ch.Get(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return r.Level
}

func (w *delegWorld) gmRefusal(id int64) string {
	w.t.Helper()
	_, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "ok"})
	var ref *chain.Refusal
	if !errors.As(err, &ref) {
		w.t.Fatalf("the Grand Master's decision on #%d = %v; want a refusal", id, err)
	}
	return ref.Code
}

// delegate is the Owner's Delegar through the flow, confirmed with the PIN.
func (w *delegWorld) delegate(id int64) {
	w.t.Helper()
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerDelegate})
	if err != nil || rep.PIN == "" {
		w.t.Fatalf("Delegar = %+v, %v; want the PIN asked", rep, err)
	}
	if err := w.usvc.CheckPIN(w.ctx, rep.PIN, delegPIN); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, w.owner.ID, rep.PIN); err != nil {
		w.t.Fatal(err)
	}
}

// CA-DELEG-01
func TestDelegarConfirmedWithThePINLetsTheGrandMasterDecideTheTableAndNoOther(t *testing.T) {
	t.Parallel()
	w := newDelegWorld(t)
	first := w.open(classes.RequestDestructive, "42-login", "")
	if k := w.at(first).Kind; k != chain.LevelPeople {
		t.Fatalf("without a delegation the request is at %s, want the people", k)
	}
	// Delegar asks the PIN and makes nothing before it
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: first, Answer: chain.AnswerDelegate})
	if err != nil || rep.PIN == "" {
		t.Fatalf("Delegar = %+v, %v; want the PIN asked", rep, err)
	}
	if ds, _ := w.ds.List(w.ctx, w.owner.ID); len(ds) != 0 {
		t.Fatalf("a decree before the PIN: %+v", ds)
	}
	// without the PIN checked, the confirmation is refused and makes nothing
	if _, err := w.flow.ConfirmPIN(w.ctx, w.owner.ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) {
		t.Fatalf("confirmation without the PIN = %v, want ErrPIN", err)
	}
	if ds, _ := w.ds.List(w.ctx, w.owner.ID); len(ds) != 0 {
		t.Fatalf("a decree without the PIN: %+v", ds)
	}
	if st := w.request(first).State; st != chain.Open {
		t.Fatalf("the request is %s without the PIN", st)
	}
	w.delegate(first)
	ds, err := w.ds.List(w.ctx, w.owner.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	d := ds[0]
	if d.Effect.Kind != decrees.EffectDelegation || d.Effect.Setting != string(classes.RequestDestructive) || d.Mesa != "42-login" || !d.PIN || d.Author != w.owner.ID {
		t.Fatalf("decree = %+v", d)
	}
	if want := w.clk.Now().Add(30 * 24 * time.Hour); !d.Until.Equal(want) {
		t.Fatalf("until = %v, want %v (30 days)", d.Until, want)
	}
	// the next request of the type in the table is the Grand Master's
	next := w.open(classes.RequestDestructive, "42-login", "")
	if k := w.at(next).Kind; k != chain.LevelGrandMaster {
		t.Fatalf("the next request of the table is at %s, want the Grand Master", k)
	}
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: next, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "dentro da delegação"})
	if err != nil || out.State != chain.Approved {
		t.Fatalf("the Grand Master's decision = %+v, %v; want approved", out, err)
	}
	// another table is the people's: the Grand Master gets human_required
	other := w.open(classes.RequestDestructive, "43-busca", "")
	if code := w.gmRefusal(other); code != chain.CodeHumanRequired {
		t.Fatalf("the Grand Master on 43-busca = %s, want human_required", code)
	}
}

// CA-DELEG-02: an Administrator's delegation of envio_workflows is refused
// before any confirmation (nothing is pending, no decree); "Delegar" on a merge
// request of risk medio, through the real flow and the PIN, makes a decree of
// policy in the type and the table of the request, and no delegation.
func TestAnAdministratorDelegatesNoHighTypeAndDelegarOnAMergeMedioIsAPolicyOfItsTable(t *testing.T) {
	t.Parallel()
	w := newDelegWorld(t)
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.ds.Propose(w.ctx, decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: w.slug, Mesa: "42-login",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: w.slug}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: string(classes.RequestWorkflows)}, Text: "delegue os workflows"})
	var ref *decrees.Refusal
	if !errors.As(err, &ref) || ref.Code != decrees.CodeLevel {
		t.Fatalf("the Administrator's delegation = %v; want a refusal for the level", err)
	}
	if ds, _ := w.ds.List(w.ctx, w.owner.ID); len(ds) != 0 {
		t.Fatalf("a refused proposal left decrees: %+v", ds)
	}
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Type: classes.RequestMerge, Workspace: w.slug, Table: "42-login", About: "mesclar a branch da mesa",
		Risk: classes.RiskResult{Risk: classes.RiskMedium, TableVersion: classes.RiskTableVersion, Rule: string(classes.RequestMerge)}, Target: delegTarget})
	if err != nil {
		t.Fatal(err)
	}
	// the Grand Master takes it up to the people, who are the ones to tap Delegar
	if _, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerClimb, Reason: "quero a decisão das pessoas"}); err != nil {
		t.Fatal(err)
	}
	w.delegate(id)
	ds, err := w.ds.List(w.ctx, w.owner.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	d := ds[0]
	if d.Effect.Kind != decrees.EffectPolicy || d.Effect.Setting != string(classes.RequestMerge) || !d.Effect.Loosens || d.Mesa != "42-login" || d.Author != w.owner.ID {
		t.Fatalf("decree = %+v; want a policy of merge in 42-login", d)
	}
}

// nopAlerts are the alerts of the bot, which these tests do not read.
type nopAlerts struct{}

func (nopAlerts) Unauthorized(context.Context, int64, string) {}
func (nopAlerts) OwnerAlert(context.Context, string)          {}
func (nopAlerts) Tell(context.Context, string, string)        {}

type nopRouter struct{}

func (nopRouter) Deliver(context.Context, telegram.Inbound) error { return nil }

// overTheBot puts the decrees over the real bot over the fake Telegram: the
// reminders of the delegations and /decretos, with the temporary table rules.
func (w *delegWorld) overTheBot() (*faketelegram.Server, *telegram.Bot) {
	w.t.Helper()
	const token = "123456:fake-bot-token"
	fake, err := faketelegram.New(w.clk, w.t, token)
	if err != nil {
		w.t.Fatal(err)
	}
	dc := &telegram.DecreeChannel{Svc: w.ds, Users: w.usvc, PINs: &telegram.PINRouter{Users: w.usvc}, Rules: telegram.TableRulesOf(w.orc.TableRules)}
	bot, err := telegram.New(telegram.Options{DB: w.ch.Store().DB, Clock: w.clk, Client: &telegram.Client{Token: token, HTTP: &http.Client{Transport: fakenet.HandlerTransport(fake)}},
		Users: w.usvc, Alerts: nopAlerts{}, Router: nopRouter{}, Commands: dc.Commands(), Actions: dc.Actions(), PollTimeout: time.Millisecond})
	if err != nil {
		w.t.Fatal(err)
	}
	dc.Bind(bot)
	w.ds.SetReminder(dc)
	return fake, bot
}

// pair pairs a user with a Telegram account of the given id.
func (w *delegWorld) pair(u users.User, chat int64) faketelegram.User {
	w.t.Helper()
	code, err := w.usvc.NewCode(w.ctx, w.owner, u.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.usvc.Pair(w.ctx, users.PairRequest{Text: code.Code, TelegramID: chat, Private: true}); err != nil {
		w.t.Fatal(err)
	}
	return faketelegram.User{ID: chat, FirstName: u.Name, Username: fmt.Sprint("u", chat)}
}

// buttonsTo are the texts of the buttons of the messages sent to a chat.
func buttonsTo(fake *faketelegram.Server, chat int64) []string {
	var out []string
	for _, s := range fake.Sent() {
		kb, ok := s.Params["reply_markup"].(map[string]any)
		if s.Method != "sendMessage" || s.ChatID != chat || !ok {
			continue
		}
		rows, _ := kb["inline_keyboard"].([]any)
		for _, row := range rows {
			cells, _ := row.([]any)
			for _, b := range cells {
				if bt, ok := b.(map[string]any); ok {
					out = append(out, fmt.Sprint(bt["text"]))
				}
			}
		}
	}
	return out
}

func (w *delegWorld) flush(bot *telegram.Bot) {
	w.t.Helper()
	for range 3 {
		w.clk.Advance(1100 * time.Millisecond)
		if _, err := bot.Flush(w.ctx); err != nil {
			w.t.Fatal(err)
		}
	}
}

// CA-DELEG-03
func TestADelegationIsRemindedOverTheBotExpiresAndLeavesTheGrandMasterHumanRequired(t *testing.T) {
	t.Parallel()
	w := newDelegWorld(t)
	fake, bot := w.overTheBot()
	chat := w.pair(w.owner, 4242).ID
	// a proposal of 31 days is refused
	_, err := w.ds.Propose(w.ctx, decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: w.slug, Mesa: "42-login", Until: w.clk.Now().Add(31 * 24 * time.Hour),
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: w.slug}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: string(classes.RequestWorkflows)}, Text: "delegue por 31 dias"})
	var ref *decrees.Refusal
	if !errors.As(err, &ref) || ref.Code != decrees.CodeDelegation {
		t.Fatalf("31 days = %v, want the refusal of the delegation", err)
	}
	// the delegation is made today
	w.delegate(w.open(classes.RequestWorkflows, "42-login", ""))
	ds, err := w.ds.List(w.ctx, w.owner.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	number := ds[0].Number
	inside := w.open(classes.RequestWorkflows, "42-login", "")
	if k := w.at(inside).Kind; k != chain.LevelGrandMaster {
		t.Fatalf("inside the delegation the request is at %s", k)
	}
	w.clk.Advance(26 * 24 * time.Hour)
	if err := w.ds.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.flush(bot)
	if got := buttonsTo(fake, chat); len(got) != 0 {
		t.Fatalf("reminded at 26 days: %v", got)
	}
	w.clk.Advance(24*time.Hour - 3*1100*time.Millisecond) // 27 days
	if err := w.ds.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.flush(bot)
	got := buttonsTo(fake, chat)
	if !slices.Contains(got, "Renovar") || !slices.Contains(got, "Deixar vencer") {
		t.Fatalf("the reminder at 27 days has the buttons %v, want Renovar and Deixar vencer", got)
	}
	// 30 days without renewing: it expires
	w.clk.Advance(3 * 24 * time.Hour)
	if err := w.ds.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if d, err := w.ds.Get(w.ctx, number); err != nil || d.State != decrees.Expired {
		t.Fatalf("after 30 days the delegation is %+v, %v; want expired", d, err)
	}
	after := w.open(classes.RequestWorkflows, "42-login", "")
	if code := w.gmRefusal(after); code != chain.CodeHumanRequired {
		t.Fatalf("the Grand Master after 30 days = %s, want human_required", code)
	}
}

// CA-DELEG-04
func TestTheSubdelegatedMestreDaMesaDecidesAWorkflowsRequestOfItsTable(t *testing.T) {
	t.Parallel()
	w := newDelegWorld(t)
	dev := w.recruit("Dev")
	table := w.table.ID
	w.delegate(w.open(classes.RequestWorkflows, table, ""))
	ds, err := w.ds.List(w.ctx, w.owner.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	origin := ds[0].Number
	before := w.open(classes.RequestWorkflows, table, dev.ID)
	if k := w.at(before).Kind; k == chain.LevelMesa {
		t.Fatal("the Mestre da mesa has a type alto without a subdelegation")
	}
	gm, master := w.get(w.gm.ID), w.get(w.master.ID)
	if gm.Role != characters.RoleGrandMaster || master.Role != characters.RoleTableMaster {
		t.Fatalf("roles = %s, %s", gm.Role, master.Role)
	}
	ask := func(by, to characters.Character, mesa string, d time.Duration) (decrees.Sub, error) {
		return w.ds.Subdelegate(w.ctx, decrees.SubRequest{By: by, Grantee: to, Mesa: mesa, Type: string(classes.RequestWorkflows), Until: w.clk.Now().Add(d), Reason: "workflows da mesa"})
	}
	code := func(_ decrees.Sub, err error) string {
		t.Helper()
		var ref *decrees.Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("err = %v, want a refusal", err)
		}
		return ref.Code
	}
	// another table, 40 days, or the Mestre da mesa subdelegating: refused
	other := master
	other.Table = "43-busca"
	if c := code(ask(gm, other, "43-busca", 2*24*time.Hour)); c != decrees.CodeTooWide {
		t.Fatalf("the table 43-busca = %s", c)
	}
	if c := code(ask(gm, master, table, 40*24*time.Hour)); c != decrees.CodeTooWide {
		t.Fatalf("40 days = %s", c)
	}
	if c := code(ask(master, master, table, 2*24*time.Hour)); c != decrees.CodeLevelDenied {
		t.Fatalf("the Mestre da mesa subdelegating = %s, want level_denied", c)
	}
	sub, err := ask(gm, master, table, 2*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id := w.open(classes.RequestWorkflows, table, dev.ID)
	if l := w.at(id); l.Kind != chain.LevelMesa || l.Agent != master.ID {
		t.Fatalf("the request is at %+v, want the Mestre da mesa", l)
	}
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: master.ID, Answer: chain.AnswerApprove, Reason: "dentro da subdelegação"})
	if err != nil || out.State != chain.Approved {
		t.Fatalf("the Mestre da mesa's decision = %+v, %v; want approved", out, err)
	}
	if n, err := w.ds.SubUses(w.ctx, sub.ID); err != nil || n != 1 {
		t.Fatalf("uses of the subdelegation = %d, %v", n, err)
	}
	// the Owner revokes the origin: the subdelegation ends with it
	if err := w.ds.Revoke(w.ctx, origin, w.owner.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := w.ds.Sub(w.ctx, sub.ID); err != nil || got.Active {
		t.Fatalf("the subdelegation after the origin = %+v, %v", got, err)
	}
	if l := w.at(w.open(classes.RequestWorkflows, table, dev.ID)); l.Kind == chain.LevelMesa {
		t.Fatal("the Mestre da mesa still decides after the origin was revoked")
	}
}

// sentTo are the messages sent to a chat, with the buttons of each by text.
func sentTo(fake *faketelegram.Server, chat int64) (texts []string, msg int64, keys map[string]string) {
	for _, s := range fake.Sent() {
		if s.Method != "sendMessage" || s.ChatID != chat {
			continue
		}
		texts = append(texts, s.Text)
		kb, ok := s.Params["reply_markup"].(map[string]any)
		if !ok {
			continue
		}
		rows, _ := kb["inline_keyboard"].([]any)
		msg, keys = s.MessageID, map[string]string{}
		for _, row := range rows {
			cells, _ := row.([]any)
			for _, b := range cells {
				if bt, ok := b.(map[string]any); ok {
					keys[fmt.Sprint(bt["text"])] = fmt.Sprint(bt["callback_data"])
				}
			}
		}
	}
	return texts, msg, keys
}

func (w *delegWorld) say(fake *faketelegram.Server, bot *telegram.Bot, u faketelegram.User, text string) {
	w.t.Helper()
	if _, err := fake.UserSendsText(u, faketelegram.Private(u), text); err != nil {
		w.t.Fatal(err)
	}
	if err := bot.PollOnce(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	w.flush(bot)
}

// CA-DEC-11
func TestDenyAlwaysThroughTheFlowAsksNoPINAndTheTableRulesAreListedWithRevogar(t *testing.T) {
	t.Parallel()
	w := newDelegWorld(t)
	fake, bot := w.overTheBot()
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	caio, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Caio", Level: users.Approver, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []users.User{ana, caio} {
		if err := w.usvc.SetPIN(w.ctx, u.ID, delegPIN); err != nil {
			t.Fatal(err)
		}
	}
	anaTG, caioTG := w.pair(ana, 5001), w.pair(caio, 5002)

	// "Negar sempre" of an Administrator, through the flow: no PIN, the decree is born
	dev := w.recruit("Dev")
	act := classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "curl https://exemplo.io/x"}
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Workspace: w.slug, Requester: dev.ID, Action: &act, About: "curl https://exemplo.io/x",
		Risk: classes.RiskResult{Risk: classes.RiskHigh, TableVersion: classes.RiskTableVersion, Rule: "rede"}})
	if err != nil {
		t.Fatal(err)
	}
	if k := w.at(id).Kind; k != chain.LevelPeople {
		t.Fatalf("the request is at %s, want the people", k)
	}
	rep, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerDenyAlways, Pattern: "curl *"})
	if err != nil || rep.PIN != "" {
		t.Fatalf("Negar sempre = %+v, %v; want it applied without the PIN", rep, err)
	}
	if st := w.request(id).State; st != chain.Denied {
		t.Fatalf("the request is %s, want denied", st)
	}
	ds, err := w.ds.List(w.ctx, ana.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	if d := ds[0]; d.PIN || d.Kind != decrees.KindRule || !d.Until.IsZero() || d.Effect.Deny == nil || !slices.Equal(d.Effect.Deny.Commands, []string{"curl *"}) || d.Author != ana.ID {
		t.Fatalf("the decree of Negar sempre = %+v", d)
	}
	// a permanent decree that frees asks the PIN
	pend, err := w.ds.Propose(w.ctx, decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: w.slug, Target: decrees.Target{Kind: decrees.TargetClass, Name: "dev"},
		Effect: decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: []string{"npm install *"}}}, Text: "o dev pode npm install"})
	if err != nil || !pend.PIN {
		t.Fatalf("a permanent decree that frees = %+v, %v; want the PIN asked", pend, err)
	}

	// a temporary table rule active is listed by /decretos, with Revogar for the Administrator only
	rule, err := (&tablerules.Store{DB: w.ch.Store().DB}).Insert(w.ctx, tablerules.Rule{Workspace: w.slug, Table: w.table.ID, Class: "dev", Request: id, State: tablerules.Active, Level: chain.LevelMesa,
		Author: w.master.ID, Risk: classes.RiskLow, Pattern: "make *", ToolKind: providers.KindShell, Until: w.clk.Now().Add(3 * time.Hour), CreatedAt: w.clk.Now()}, "teste")
	if err != nil {
		t.Fatal(err)
	}
	live := func() int {
		t.Helper()
		got, err := (&tablerules.Store{DB: w.ch.Store().DB}).Live(w.ctx, w.slug)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	w.say(fake, bot, caioTG, "/decretos")
	texts, _, keys := sentTo(fake, caioTG.ID)
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], fmt.Sprintf("regra temporária de mesa %d", rule.ID)) {
		t.Fatalf("/decretos says to the Approver %q", texts)
	}
	if len(keys) != 0 {
		t.Fatalf("an Approver was given %v", keys)
	}
	w.say(fake, bot, anaTG, "/decretos")
	texts, msg, keys := sentTo(fake, anaTG.ID)
	revoke := fmt.Sprintf("Revogar regra de mesa %d", rule.ID)
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], fmt.Sprintf("regra temporária de mesa %d", rule.ID)) || keys[revoke] == "" {
		t.Fatalf("/decretos says to the Administrator %q with %v", texts, keys)
	}
	// the tap of an Approver is refused: Ana is an Approver when she taps
	approver := users.Approver
	if _, err := w.usvc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: &approver}); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.UserClicks(anaTG, msg, keys[revoke]); err != nil {
		t.Fatal(err)
	}
	if err := bot.PollOnce(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.flush(bot)
	if n := live(); n != 1 {
		t.Fatalf("the tap of an Approver revoked the table rule: %d live", n)
	}
	// the same tap by the Administrator revokes it
	admin := users.Administrator
	if _, err := w.usvc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: &admin}); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.UserClicks(anaTG, msg, keys[revoke]); err != nil {
		t.Fatal(err)
	}
	if err := bot.PollOnce(w.ctx); err != nil {
		t.Fatal(err)
	}
	if n := live(); n != 0 {
		t.Fatalf("the Administrator did not revoke the table rule: %d live", n)
	}
}
