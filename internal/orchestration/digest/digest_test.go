package digest_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/digest"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
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

type rules struct {
	mu      sync.Mutex
	revoked []int64
}

func (r *rules) RevokeOfRequest(_ context.Context, _, _ string, request int64, _ string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revoked = append(r.revoked, request)
	return 1, nil
}

type alerts struct{}

func (alerts) Unauthorized(context.Context, int64, string) {}
func (alerts) OwnerAlert(context.Context, string)          {}
func (alerts) Tell(context.Context, string, string)        {}

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

// CA-APROV-RES-03: interpreters are not suggested with a wildcard.
func TestNoSuggestionForAnInterpreterWithAWildcard(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1)
	w.tick()
	for range 3 {
		w.decided(chain.LevelMesa, true, "42-login", "dev", "python script.py", classes.RiskLow)
		w.decided(chain.LevelMesa, true, "42-login", "dev", "sh -c x", classes.RiskLow)
	}
	w.toSlot()
	w.tick()
	all, _ := w.svc.Suggestions(w.ctx, "loja", "")
	for _, x := range all {
		if x.Pattern == "python *" || x.Pattern == "sh *" || strings.HasPrefix(x.Pattern, "sh ") {
			t.Fatalf("suggested %q", x.Pattern)
		}
	}
}

// CA-APROV-RES-04: the sample of a level with more than five has five, the same
// every run with the same seed, sent by the engine; "Foi um erro" is recorded
// and revokes the temporary rule.
func TestTheSampleIsDrawnWithTheSeedSentByTheEngineAndAMistakeRevokes(t *testing.T) {
	t.Parallel()
	drawn := func(seed uint64) (*world, []string) {
		w := newWorld(t, seed)
		w.tick()
		for i := range 12 {
			w.decided(chain.LevelMesa, true, "42-login", "dev", fmt.Sprintf("make target%d x", i), classes.RiskLow)
		}
		w.toSlot()
		w.tick()
		var ids []string
		for _, m := range w.send.to(w.owner.ID) {
			if strings.HasPrefix(m.Text, "Amostra para revisão") {
				for _, line := range strings.Split(m.Text, "\n") {
					if strings.HasPrefix(line, "#") {
						ids = append(ids, strings.SplitN(line, " ", 2)[0])
					}
				}
			}
		}
		return w, ids
	}
	w, ids := drawn(7)
	if len(ids) != 5 {
		t.Fatalf("sample = %v", ids)
	}
	if _, again := drawn(7); strings.Join(again, ",") != strings.Join(ids, ",") {
		t.Fatalf("the same seed drew %v and %v", ids, again)
	}
	var mistake string
	for _, m := range w.send.to(w.owner.ID) {
		for _, row := range m.Buttons {
			for _, b := range row {
				if b.Action == digest.MistakeAction {
					mistake = b.Arg
				}
			}
		}
	}
	if mistake == "" {
		t.Fatal("no button Foi um erro for the Owner")
	}
	var id int64
	_, _ = fmt.Sscan(mistake, &id)
	if _, err := w.svc.Mark(w.ctx, id, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.rules.revoked) != 1 || w.rules.revoked[0] != id {
		t.Fatalf("revoked = %v", w.rules.revoked)
	}
	audit, _ := w.st.AuditOf(w.ctx, id)
	if last := audit[len(audit)-1]; last.Answer != "marked_error" || last.Approver != w.owner.ID {
		t.Fatalf("audit = %+v", audit)
	}
}

// CA-APROV-RES-05: an "aprovar sempre" of the Mestre da mesa is a suggestion with
// the reason of the agent, and no decree exists before the Aceitar with the PIN.
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

// CA-APROV-RES-06: eight actions of the Mestres de mesa and two of the Grão-Mestre
// give five of the first and both of the second, in separate parts, each with
// level and risk.
func TestTheSampleIsPerLevelInSeparateParts(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 3)
	w.tick()
	for i := range 8 {
		risk := classes.RiskLow
		if i < 3 {
			risk = classes.RiskMedium
		}
		w.decided(chain.LevelMesa, true, "42-login", "dev", fmt.Sprintf("make m%d x", i), risk)
	}
	for i := range 2 {
		w.decided(chain.LevelGrandMaster, true, "42-login", "dev", fmt.Sprintf("make g%d x", i), classes.RiskMedium)
	}
	w.toSlot()
	w.tick()
	var mesa, gm int
	parts := 0
	for _, text := range w.ownerTexts() {
		if !strings.HasPrefix(text, "Amostra para revisão") {
			continue
		}
		parts++
		n := strings.Count(text, "\n#")
		switch {
		case strings.Contains(text, "por Mestre da mesa"):
			mesa = n
		case strings.Contains(text, "por Grão-Mestre"):
			gm = n
		}
		if !strings.Contains(text, "nível") || !strings.Contains(text, "risco") {
			t.Fatalf("no level or risk in %q", text)
		}
	}
	if parts != 2 || mesa != 5 || gm != 2 {
		t.Fatalf("parts = %d, mesa = %d, gm = %d", parts, mesa, gm)
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
