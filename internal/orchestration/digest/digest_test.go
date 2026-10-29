package digest_test

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const pin = "482913"

type inbox struct {
	mu    sync.Mutex
	items []string
}

func (i *inbox) ToGrandMaster(_ context.Context, _, text string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.items = append(i.items, text)
	return nil
}

type sender struct {
	mu   sync.Mutex
	sent []digest.Message
}

func (s *sender) Send(_ context.Context, m digest.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}

func (s *sender) to(user string) []digest.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []digest.Message
	for _, m := range s.sent {
		if m.UserID == user {
			out = append(out, m)
		}
	}
	return out
}

// rules is the revocation of the temporary rules of the Citadel over the real
// service of the table rules (the same adapter the Citadel wires), which also
// records the requests it was asked to revoke.
type rules struct {
	mu      sync.Mutex
	revoked []int64
	real    *tablerules.Service
}

func (r *rules) RevokeOfRequest(ctx context.Context, workspace, table string, request int64, by string) (int, error) {
	r.mu.Lock()
	r.revoked = append(r.revoked, request)
	r.mu.Unlock()
	list, err := r.real.List(ctx, workspace, table)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rule := range list {
		if rule.Request != request {
			continue
		}
		if err := r.real.Revoke(ctx, rule.ID, tablerules.Revoker{Administrator: true, ID: by}); err == nil {
			n++
		}
	}
	return n, nil
}

// noCharacters and noClasses are the pieces of the table rules that the
// creation and the revocation of a rule do not use.
type noCharacters struct{ chain.Characters }
type noClasses struct{ chain.Classes }

type alerts struct{}

func (alerts) Unauthorized(context.Context, int64, i18n.Msg) {}
func (alerts) OwnerAlert(context.Context, i18n.Msg)          {}
func (alerts) Tell(context.Context, string, i18n.Msg)        {}

type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	st    *chain.Store
	svc   *digest.Service
	ds    *decrees.Service
	users *users.Service
	owner users.User
	admin users.User
	inbox *inbox
	send  *sender
	rules *rules
	// trules is the real service of the temporary table rules.
	trules *tablerules.Service
}

func newWorld(t *testing.T, seed uint64) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ctx: ctx, clk: clk, db: db, st: &chain.Store{DB: db}, inbox: &inbox{}, send: &sender{}, rules: &rules{}}
	if w.trules, err = tablerules.NewService(tablerules.Options{Store: &tablerules.Store{DB: db}, Clock: clk, Characters: noCharacters{}, Policy: chain.StaticPolicy{}, Classes: noClasses{}}); err != nil {
		t.Fatal(err)
	}
	w.rules.real = w.trules
	w.users, err = users.New(users.Options{DB: db, Clock: clk, Alerts: alerts{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if w.owner, err = w.users.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	if err := w.users.SetPIN(ctx, w.owner.ID, pin); err != nil {
		t.Fatal(err)
	}
	if w.admin, err = w.users.Create(ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{"loja"}}); err != nil {
		t.Fatal(err)
	}
	w.ds, err = decrees.New(decrees.Options{DB: db, Clock: clk, PINs: w.users})
	if err != nil {
		t.Fatal(err)
	}
	w.svc, err = digest.New(digest.Options{DB: db, Clock: clk, Source: w.st, Policy: chain.StaticPolicy{}, Inbox: w.inbox, Directory: digest.UsersDirectory{Users: w.users},
		Sender: w.send, Rand: rand.New(rand.NewPCG(seed, seed)), Decrees: w.ds, Rules: w.rules})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// decided makes a request an agent of the level approved (or denied) now.
func (w *world) decided(level chain.LevelKind, approved bool, table, class, command string, risk classes.Risk) chain.Request {
	w.t.Helper()
	r := chain.Request{Workspace: "loja", Table: table, Type: classes.RequestClassPermission, State: chain.Approved, Class: class, About: command,
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: command}, Risk: classes.RiskResult{Risk: risk}, CreatedAt: w.clk.Now()}
	if !approved {
		r.State = chain.Denied
	}
	r, err := w.st.Insert(w.ctx, r)
	if err != nil {
		w.t.Fatal(err)
	}
	r.ClosedAt = w.clk.Now()
	if err := w.st.Save(w.ctx, r); err != nil {
		w.t.Fatal(err)
	}
	ans := chain.AnswerApprove
	if !approved {
		ans = chain.AnswerDeny
	}
	if err := w.st.Audit(w.ctx, chain.AuditEntry{Request: r.ID, At: w.clk.Now(), Approver: fmt.Sprintf("agent-%s", level), Level: level, Answer: ans, Outcome: "accepted", Reason: "ok " + command}); err != nil {
		w.t.Fatal(err)
	}
	w.clk.Advance(time.Minute)
	return r
}

func (w *world) tick() {
	w.t.Helper()
	if err := w.svc.Tick(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// toSlot advances the clock to just after the next 18:00.
func (w *world) toSlot() {
	w.t.Helper()
	now := w.clk.Now()
	slot := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 30, 0, time.UTC)
	if !slot.After(now) {
		slot = slot.AddDate(0, 0, 1)
	}
	w.clk.Advance(slot.Sub(now))
}

func (w *world) ownerTexts() []string {
	var out []string
	for _, m := range w.send.to(w.owner.ID) {
		out = append(out, m.Text)
	}
	return out
}

// CA-APROV-RES-01: at the time of the summary the Grand Master gets the list of
// what its agents decided, with level and risk; a day with no decision sends nothing.
func TestTheSummaryListGoesToTheGrandMasterAtTheTimeAndNothingOnAQuietDay(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick() // the first sight of the workspace only notes the slot
	for i := range 3 {
		w.decided(chain.LevelGrandMaster, true, "42-login", "dev", fmt.Sprintf("make step%d now", i), classes.RiskMedium)
	}
	for i := range 2 {
		w.decided(chain.LevelMesa, true, "42-login", "dev", fmt.Sprintf("go test pkg%d now", i), classes.RiskLow)
	}
	w.tick()
	if len(w.inbox.items) != 0 {
		t.Fatalf("sent before the time: %v", w.inbox.items)
	}
	w.toSlot()
	w.tick()
	if len(w.inbox.items) != 1 {
		t.Fatalf("inbox = %v", w.inbox.items)
	}
	text := w.inbox.items[0]
	if strings.Count(text, "\n- #") != 5 || !strings.Contains(text, "Grão-Mestre") || !strings.Contains(text, "Mestre da mesa") || !strings.Contains(text, "risco medio") || !strings.Contains(text, "risco baixo") {
		t.Fatalf("the list = %q", text)
	}
	w.tick()
	if len(w.inbox.items) != 1 {
		t.Fatal("the same slot was sent twice")
	}
	w.clk.Advance(24 * time.Hour)
	w.tick()
	if len(w.inbox.items) != 1 {
		t.Fatalf("a day with no decisions sent %v", w.inbox.items[1:])
	}
}

// CA-APROV-RES-02: three approvals of npm install <package> in a week become a
// suggestion to the Owner; Aceitar with the PIN makes a decree of the Owner for
// the class; Recusar keeps the pattern away for 30 days.
func TestRepeatedApprovalsBecomeASuggestionAcceptedWithThePIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	for _, pkg := range []string{"lodash", "react", "left-pad"} {
		w.decided(chain.LevelMesa, true, "42-login", "dev", "npm install "+pkg, classes.RiskLow)
	}
	w.toSlot()
	w.tick()
	var sug *digest.Message
	for _, m := range w.send.to(w.owner.ID) {
		if strings.Contains(m.Text, "Sugestão de regra") {
			sug = &m
		}
	}
	if sug == nil {
		t.Fatalf("no suggestion for the Owner: %v", w.ownerTexts())
	}
	for _, want := range []string{"`npm install *`", "classe dev", "npm install lodash", "npm install react", "npm install left-pad"} {
		if !strings.Contains(sug.Text, want) {
			t.Fatalf("the suggestion lacks %q: %q", want, sug.Text)
		}
	}
	keys := map[string]bool{}
	for _, b := range sug.Buttons[0] {
		keys[b.Text] = true
	}
	if !keys["Aceitar"] || !keys["Recusar"] || !keys["Ver exemplos"] {
		t.Fatalf("buttons = %+v", sug.Buttons)
	}
	if len(w.send.to(w.admin.ID)) != 0 {
		for _, m := range w.send.to(w.admin.ID) {
			if strings.Contains(m.Text, "Sugestão") {
				t.Fatal("a suggestion went to somebody who is not the Owner")
			}
		}
	}
	list, _ := w.svc.Suggestions(w.ctx, "loja", "open")
	if len(list) != 1 {
		t.Fatalf("suggestions = %+v", list)
	}
	if _, err := w.svc.Accept(w.ctx, list[0].ID, w.admin.ID); err != digest.ErrNotOwner {
		t.Fatalf("an Administrator accepted: %v", err)
	}
	if more, err := w.svc.ExamplesText(w.ctx, list[0].ID); err != nil || !strings.Contains(more, "Libera a mais") {
		t.Fatalf("examples = %q, %v", more, err)
	}
	pend, err := w.svc.Accept(w.ctx, list[0].ID, w.owner.ID)
	if err != nil || !pend.PIN {
		t.Fatalf("accept = %+v, %v", pend, err)
	}
	var n int
	_ = w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decrees`).Scan(&n)
	if n != 0 {
		t.Fatal("a decree exists before the PIN")
	}
	req, err := w.ds.BeginPIN(w.ctx, pend.ID, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, req.ID, pin); err != nil {
		t.Fatal(err)
	}
	d, err := w.ds.Confirm(w.ctx, pend.ID, w.owner.ID, req.ID)
	if err != nil || d.Target.Name != "dev" || d.Author != w.owner.ID || d.Effect.Allow == nil || d.Effect.Allow.Commands[0] != "npm install *" {
		t.Fatalf("decree = %+v, %v", d, err)
	}
}

// CA-APROV-RES-02, the half of Recusar (30 days) and of the week
func TestADeclinedPatternComesBackOnlyAfterThirtyDays(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	approve := func() {
		for _, pkg := range []string{"a", "b", "c"} {
			w.decided(chain.LevelMesa, true, "42-login", "dev", "npm install "+pkg, classes.RiskLow)
		}
	}
	approve()
	w.toSlot()
	w.tick()
	list, _ := w.svc.Suggestions(w.ctx, "loja", "open")
	if len(list) != 1 {
		t.Fatalf("suggestions = %+v", list)
	}
	if err := w.svc.Decline(w.ctx, list[0].ID, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	count := func() int { l, _ := w.svc.Suggestions(w.ctx, "loja", ""); return len(l) }
	w.clk.Advance(2 * 24 * time.Hour)
	approve()
	w.toSlot()
	w.tick()
	if count() != 1 {
		t.Fatalf("a declined pattern came back at once: %d", count())
	}
	w.clk.Advance(31 * 24 * time.Hour)
	approve()
	w.toSlot()
	w.tick()
	if count() != 2 {
		t.Fatalf("the pattern did not come back after 30 days: %d", count())
	}
}

// CA-APROV-RES-02, the window of 7 days: three approvals spread over more than
// a week are not a pattern.
func TestApprovalsSpreadOverMoreThanAWeekMakeNoSuggestion(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	for _, pkg := range []string{"a", "b", "c"} {
		w.decided(chain.LevelMesa, true, "42-login", "dev", "npm install "+pkg, classes.RiskLow)
		w.clk.Advance(4 * 24 * time.Hour)
	}
	w.toSlot()
	w.tick()
	if list, _ := w.svc.Suggestions(w.ctx, "loja", ""); len(list) != 0 {
		t.Fatalf("suggestions = %+v; want none for approvals spread over 12 days", list)
	}
}

// CA-APROV-RES-03: no suggestion is made for `python *` nor `sh *` (nor for the
// other interpreters), while a command that is safe to generalize is suggested
// in the same run (the control that the engine looks).
func TestNoSuggestionForAnInterpreterWithAWildcard(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	// the two of the criterion, and the forms of the other interpreters
	forms := []string{"python script.py", "sh -c x", "bash -c x", "python -c x", "node -e x", "ruby -e x", "perl -e x", "node app.js", "ruby run.rb"}
	for range 3 {
		for _, cmd := range forms {
			w.decided(chain.LevelMesa, true, "42-login", "dev", cmd, classes.RiskLow)
		}
		w.decided(chain.LevelMesa, true, "42-login", "dev", "make lint all", classes.RiskLow)
	}
	w.toSlot()
	w.tick()
	all, err := w.svc.Suggestions(w.ctx, "loja", "")
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, x := range all {
		patterns = append(patterns, x.Pattern)
	}
	if !slices.Contains(patterns, "make lint *") {
		t.Fatalf("suggested patterns = %q, want make lint * (a safe pattern is suggested in the same run)", patterns)
	}
	for _, bad := range []string{"python *", "sh *", "bash *", "node *", "ruby *", "perl *"} {
		if slices.Contains(patterns, bad) {
			t.Errorf("suggested %q for an interpreter", bad)
		}
	}
	// and the table of risk would not take them as a rule either
	for _, prog := range []string{"python", "sh", "bash", "node", "ruby", "perl"} {
		if _, err := classes.CheckApprovablePattern(prog+" *", classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: prog + " x y"}, "dev", "42-login"); err == nil {
			t.Errorf("the pattern %q is approvable", prog+" *")
		}
	}
}

// tempRule makes the temporary rule that the approval of a request created, in
// the real service of the table rules, and returns its id.
func (w *world) tempRule(r chain.Request) int64 {
	w.t.Helper()
	err := w.trules.Create(w.ctx, chain.TempRule{Request: r.ID, Workspace: r.Workspace, Table: r.Table, Class: r.Class, Risk: classes.RiskLow, Action: *r.Action,
		Pattern: &classes.ApprovablePattern{Pattern: "make target *", Kind: providers.KindShell, Class: r.Class, Table: r.Table, Risk: classes.RiskResult{Risk: classes.RiskLow}},
		Until:   w.clk.Now().Add(2 * time.Hour), By: "agent-mesa", Level: chain.Level{Kind: chain.LevelMesa, Agent: "agent-mesa"}})
	if err != nil {
		w.t.Fatal(err)
	}
	list, err := w.trules.List(w.ctx, r.Workspace, r.Table)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, rule := range list {
		if rule.Request == r.ID {
			return rule.ID
		}
	}
	w.t.Fatalf("no temporary rule of the request #%d", r.ID)
	return 0
}

// samples reads the parts of sample the Owner got: the request numbers of each
// level, by the word of the level in the message.
func (w *world) samples() map[string][]int64 {
	out := map[string][]int64{}
	for _, text := range w.ownerTexts() {
		if !strings.HasPrefix(text, "Amostra para revisão") {
			continue
		}
		level := "Mestre da mesa"
		if strings.Contains(text, "por Grão-Mestre") {
			level = "Grão-Mestre"
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "#") {
				var id int64
				_, _ = fmt.Sscan(strings.TrimPrefix(strings.SplitN(line, " ", 2)[0], "#"), &id)
				out[level] = append(out[level], id)
			}
		}
	}
	return out
}

// CA-APROV-RES-04: twelve actions approved by the Mestres de mesa and twelve by
// the Grão-Mestre give five of each level, the same every run with the same
// seed, sent by the engine although the Grão-Mestre never writes a word about
// them; "Foi um erro" on one that made a temporary rule leaves the rule
// revogada (in the real service of the rules) and puts the mark in the audit.
func TestTheSampleIsDrawnWithTheSeedSentByTheEngineAndAMistakeRevokes(t *testing.T) {
	t.Parallel()
	type drawn struct {
		w     *world
		made  map[string]map[int64]bool // the approved requests of each level
		rules map[int64]int64           // request -> temporary rule
		got   map[string][]int64
	}
	draw := func(seed uint64) drawn {
		w := newWorld(t, seed)
		w.tick()
		d := drawn{w: w, made: map[string]map[int64]bool{"Mestre da mesa": {}, "Grão-Mestre": {}}, rules: map[int64]int64{}}
		for i := range 12 {
			r := w.decided(chain.LevelMesa, true, "42-login", "dev", fmt.Sprintf("make target%d x", i), classes.RiskLow)
			d.made["Mestre da mesa"][r.ID] = true
			d.rules[r.ID] = w.tempRule(r)
			r = w.decided(chain.LevelGrandMaster, true, "42-login", "dev", fmt.Sprintf("make gm%d x", i), classes.RiskMedium)
			d.made["Grão-Mestre"][r.ID] = true
		}
		w.toSlot()
		w.tick()
		d.got = w.samples()
		return d
	}
	d := draw(7)
	for level, ids := range d.got {
		seen := map[int64]bool{}
		for _, id := range ids {
			if !d.made[level][id] || seen[id] {
				t.Errorf("%s: the sample %v has #%d, that is not one of its twelve (or twice)", level, ids, id)
			}
			seen[id] = true
		}
	}
	if len(d.got["Mestre da mesa"]) != 5 || len(d.got["Grão-Mestre"]) != 5 || len(d.got) != 2 {
		t.Fatalf("samples = %v, want five actions of each of the two levels", d.got)
	}
	// the Grão-Mestre was only given the list, and wrote nothing to anyone: the
	// sample reached the Owner all the same, with the pedido of each action
	if len(d.w.inbox.items) != 1 {
		t.Fatalf("the Grand Master got %d lists", len(d.w.inbox.items))
	}
	for _, text := range d.w.ownerTexts() {
		if strings.HasPrefix(text, "Amostra para revisão") && strings.Count(text, "  pedido: make ") != 5 {
			t.Errorf("the sample does not carry the five actions: %q", text)
		}
	}
	if again := draw(7); !maps.EqualFunc(again.got, d.got, slices.Equal[[]int64]) {
		t.Fatalf("the same seed drew %v and %v", d.got, again.got)
	}
	// the button of an action of the sample makes the mistake
	w := d.w
	buttons := map[int64]bool{}
	for _, m := range w.send.to(w.owner.ID) {
		for _, row := range m.Buttons {
			for _, b := range row {
				if b.Action == digest.MistakeAction {
					var id int64
					_, _ = fmt.Sscan(b.Arg, &id)
					buttons[id] = true
				}
			}
		}
	}
	if len(buttons) != 10 {
		t.Fatalf("buttons Foi um erro = %v, want one for each of the ten actions", buttons)
	}
	// and each message of the sample has the button Pausar autonomia of the table it names
	pauses := 0
	for _, m := range w.send.to(w.owner.ID) {
		if !strings.HasPrefix(m.Text, "Amostra para revisão") {
			continue
		}
		for _, row := range m.Buttons {
			for _, b := range row {
				if b.Action == digest.PauseAutonomyAction && b.Arg == "loja:42-login" {
					pauses++
				}
			}
		}
	}
	if pauses != 2 {
		t.Fatalf("%d buttons Pausar autonomia in the two messages of the sample, want one in each", pauses)
	}
	var id int64
	for _, got := range d.got["Mestre da mesa"] {
		id = got // the sample is sorted: the last is a stable choice
	}
	if !buttons[id] || d.rules[id] == 0 {
		t.Fatalf("the action #%d has no button or no rule", id)
	}
	if st, _ := w.trules.Get(w.ctx, d.rules[id]); st.State != tablerules.Active {
		t.Fatalf("the rule is %s before the mistake, want active", st.State)
	}
	n, err := w.svc.Mark(w.ctx, id, w.owner.ID)
	if err != nil || n != 1 {
		t.Fatalf("Mark = %d, %v, want the one rule revoked", n, err)
	}
	for req, rule := range d.rules {
		got, err := w.trules.Get(w.ctx, rule)
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]tablerules.State{true: tablerules.Revoked, false: tablerules.Active}[req == id]; got.State != want {
			t.Errorf("the rule of #%d is %s, want %s", req, got.State, want)
		}
	}
	audit, _ := w.st.AuditOf(w.ctx, id)
	if last := audit[len(audit)-1]; last.Answer != "marked_error" || last.Approver != w.owner.ID {
		t.Fatalf("audit = %+v", audit)
	}
}

// Part of CA-APROV-RES-05 (the decision that makes the "sempre" is real in
// internal/orchestration/chain): a suggestion of an "aprovar sempre" reaches the
// Owner with the reason of the agent, and no decree exists before the Aceitar.
func TestAnAlwaysOfAnAgentIsASuggestionWithItsReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	r := w.decided(chain.LevelMesa, true, "42-login", "dev", "npm install lodash", classes.RiskLow)
	if err := w.st.AddSuggestion(w.ctx, chain.Suggestion{Request: r.ID, At: w.clk.Now(), Kind: chain.AnswerApproveAlways, By: "agent-mesa", Class: "dev", Action: "npm install lodash", Reason: "são só dependências de dev"}); err != nil {
		t.Fatal(err)
	}
	w.toSlot()
	w.tick()
	found := false
	for _, text := range w.ownerTexts() {
		found = found || (strings.Contains(text, "Sugestão de regra") && strings.Contains(text, "são só dependências de dev") && strings.Contains(text, "texto do agente, não verificado"))
	}
	if !found {
		t.Fatalf("the Owner did not get the reason: %v", w.ownerTexts())
	}
	var n int
	_ = w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decrees`).Scan(&n)
	if n != 0 {
		t.Fatal("a decree exists before the Aceitar")
	}
}

// CA-APROV-RES-06: eight actions of the Mestres de mesa (three medio) and two of
// the Grão-Mestre give five of the first, drawn among the eight, and both of the
// second, in separate parts, each action with its level and its risk.
func TestTheSampleIsPerLevelInSeparateParts(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 3)
	w.tick()
	type action struct {
		command string
		risk    classes.Risk
		level   string
	}
	made := map[int64]action{}
	for i := range 8 {
		risk := classes.RiskLow
		if i < 3 {
			risk = classes.RiskMedium
		}
		r := w.decided(chain.LevelMesa, true, "42-login", "dev", fmt.Sprintf("make m%d x", i), risk)
		made[r.ID] = action{fmt.Sprintf("make m%d x", i), risk, "Mestre da mesa"}
	}
	for i := range 2 {
		r := w.decided(chain.LevelGrandMaster, true, "42-login", "dev", fmt.Sprintf("make g%d x", i), classes.RiskMedium)
		made[r.ID] = action{fmt.Sprintf("make g%d x", i), classes.RiskMedium, "Grão-Mestre"}
	}
	w.toSlot()
	w.tick()
	var parts []string
	got := map[string][]int64{}
	for _, text := range w.ownerTexts() {
		if !strings.HasPrefix(text, "Amostra para revisão") {
			continue
		}
		parts = append(parts, text)
		level := "Mestre da mesa"
		if strings.Contains(text, "por Grão-Mestre") {
			level = "Grão-Mestre"
		}
		if strings.Contains(text, "por Mestre da mesa") == strings.Contains(text, "por Grão-Mestre") {
			t.Fatalf("a part that is of both levels or of none: %q", text)
		}
		blocks := strings.Split(text, "\n#")[1:]
		for _, block := range blocks {
			var id int64
			_, _ = fmt.Sscan(strings.SplitN(block, " ", 2)[0], &id)
			a, ok := made[id]
			if !ok || a.level != level {
				t.Fatalf("the %s part has #%d, that is not an action of the level: %q", level, id, text)
			}
			head := strings.SplitN(block, "\n", 2)[0]
			if !strings.Contains(head, "nível "+level) || !strings.Contains(head, "risco "+string(a.risk)) {
				t.Errorf("the line of #%d = %q, want level %s and risk %s", id, head, level, a.risk)
			}
			if !strings.Contains(block, "pedido: "+a.command) {
				t.Errorf("the block of #%d does not say its action %q: %q", id, a.command, block)
			}
			got[level] = append(got[level], id)
		}
	}
	if len(parts) != 2 || len(got["Mestre da mesa"]) != 5 || len(got["Grão-Mestre"]) != 2 {
		t.Fatalf("parts = %d, sample = %v, want two parts with five of the Mestres de mesa and the two of the Grão-Mestre", len(parts), got)
	}
	seen := map[int64]bool{}
	for _, id := range got["Mestre da mesa"] {
		if seen[id] {
			t.Errorf("#%d is in the sample twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 5 {
		t.Errorf("the five are %v", got["Mestre da mesa"])
	}
	// both actions of the Grão-Mestre, each once
	wantGM := map[int64]bool{}
	for id, a := range made {
		if a.level == "Grão-Mestre" {
			wantGM[id] = true
		}
	}
	gotGM := map[int64]bool{}
	for _, id := range got["Grão-Mestre"] {
		gotGM[id] = true
	}
	if !maps.Equal(gotGM, wantGM) {
		t.Errorf("the Grão-Mestre part has %v; want exactly its two actions %v", got["Grão-Mestre"], wantGM)
	}
}

func TestTheSecretsOfTheSummaryAreMasked(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	r := w.decided(chain.LevelMesa, true, "42-login", "dev", "make deploy now", classes.RiskLow)
	// the reason of the agent is text of the agent: a token in it must not leave
	if err := w.st.Audit(w.ctx, chain.AuditEntry{Request: r.ID, At: w.clk.Now(), Approver: "agent-mesa", Level: chain.LevelMesa, Answer: chain.AnswerApprove, Outcome: "accepted", Reason: "token ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"}); err != nil {
		t.Fatal(err)
	}
	w.toSlot()
	w.tick()
	all := strings.Join(w.inbox.items, "\n") + strings.Join(w.ownerTexts(), "\n")
	if strings.Contains(all, "ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD") {
		t.Fatal("a secret reached the summary")
	}
}

// An accepted suggestion is not suggested again while its approvals are still in
// the window, a double tap on Aceitar makes one proposal, a second mark does
// not repeat the audit, and the sample says the total.
func TestAnAcceptedSuggestionDoesNotComeBackAndAcceptIsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	for _, pkg := range []string{"a", "b", "c"} {
		w.decided(chain.LevelMesa, true, "42-login", "dev", "npm install "+pkg, classes.RiskLow)
	}
	w.toSlot()
	w.tick()
	open, _ := w.svc.Suggestions(w.ctx, "loja", "open")
	if len(open) != 1 {
		t.Fatalf("open = %+v", open)
	}
	if _, err := w.svc.Accept(w.ctx, open[0].ID, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Accept(w.ctx, open[0].ID, w.owner.ID); err != digest.ErrNotOpen {
		t.Fatalf("the second Aceitar = %v", err)
	}
	w.clk.Advance(24 * time.Hour)
	w.tick()
	all, _ := w.svc.Suggestions(w.ctx, "loja", "")
	if len(all) != 1 {
		t.Fatalf("an accepted suggestion came back: %+v", all)
	}
	var sampleText string
	for _, text := range w.ownerTexts() {
		if strings.HasPrefix(text, "Amostra para revisão") {
			sampleText = text
		}
	}
	if !strings.Contains(sampleText, "3 de 3 do período") {
		t.Fatalf("sample header = %q", sampleText)
	}
}

func TestAMistakeMarkedTwiceIsAuditedOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	r := w.decided(chain.LevelMesa, true, "42-login", "dev", "make x y", classes.RiskLow)
	for range 2 {
		if _, err := w.svc.Mark(w.ctx, r.ID, w.owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	marks := 0
	audit, _ := w.st.AuditOf(w.ctx, r.ID)
	for _, e := range audit {
		if e.Answer == "marked_error" {
			marks++
		}
	}
	if marks != 1 || len(w.rules.revoked) != 1 {
		t.Fatalf("marks = %d, revoked = %v", marks, w.rules.revoked)
	}
}
