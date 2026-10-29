package progress_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/progress"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/pkg/clock"
)

type fakeTables struct{ ts []tables.Table }

func (f *fakeTables) List(context.Context, string) ([]tables.Table, error) { return f.ts, nil }

type fakeRequests map[string][]chain.Request

func (f fakeRequests) OpenOfTable(_ context.Context, _, table string) ([]chain.Request, error) {
	return f[table], nil
}

type fakeWorkspaces []string

func (f fakeWorkspaces) Active(context.Context) ([]string, error) { return f, nil }

type fakePeople []string

func (f fakePeople) Recipients(context.Context, string) ([]string, error) { return f, nil }

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

// text is the message as the people read it in pt-BR.
func text(m progress.Message) string { return i18n.Render(i18n.PtBR, m.Msg) }

func (f *fakeSender) Send(_ context.Context, user, _ string, msg i18n.Msg) error {
	text := i18n.Render(i18n.PtBR, msg)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, user+": "+text)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type rig struct {
	svc    *progress.Service
	clk    *clock.Fake
	tables *fakeTables
	reqs   fakeRequests
	out    *fakeSender
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{clk: clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)), tables: &fakeTables{}, reqs: fakeRequests{}, out: &fakeSender{}}
	svc, err := progress.New(progress.Options{Clock: r.clk, Tables: r.tables, Requests: r.reqs, Workspaces: fakeWorkspaces{"loja"}, Recipients: fakePeople{"u1"}, Sender: r.out})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func table(id, title string, state tables.State, merged bool) tables.Table {
	t := tables.Table{ID: id, EpicTitle: title, State: state, Merged: merged}
	if merged {
		t.PRNumber = 40
	}
	return t
}

// CA-GM-15: two tables merged and one waiting for the approval of PR #41: the
// message counts the merged ones and has one line with the owner and the motive
// of the pending one. A table whose pull request was merged on GitHub but whose
// merge the engine did not confirm in the base (CA-GH-EV-08 keeps it unmarked)
// is not counted as merged.
func TestTheProgressMessageCountsWhatTheEngineConfirmedAndNamesTheOwnerOfWhatWaits(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	checkout := table("41-checkout", "Checkout", tables.StateInReview, false)
	checkout.PRNumber = 41
	unconfirmed := table("44-busca", "Busca", tables.StateInReview, false)
	unconfirmed.PRNumber = 44 // pr_mergeado came, but not the commit in the base: Merged stays false
	r.tables.ts = []tables.Table{table("38-login", "Login", tables.StateInReview, true), table("39-recibos", "Recibos", tables.StateInReview, true), checkout, unconfirmed,
		table("30-velha", "Velha", tables.StateClosed, true)}
	r.reqs["41-checkout"] = []chain.Request{{ID: 7, Level: chain.Level{Kind: chain.LevelPeople}, PR: &chain.PRRef{Number: 41}, Why: []string{"mexe em pagamentos"}}}

	m, ok, err := r.svc.Compose(t.Context(), "loja")
	if err != nil || !ok {
		t.Fatalf("Compose = %v, %v", ok, err)
	}
	want := "loja · 4 mesas\n  ✅ 2 mescladas (Login, Recibos)\n  🔧 1 em andamento (Busca)\n  🙋 Checkout: espera você — aprovar PR #41 (mexe em pagamentos)\nPróximo: Checkout volta ao trabalho quando você aprovar"
	if text(m) != want {
		t.Errorf("message:\n%s\nwant:\n%s", text(m), want)
	}
}

// The lines of the message of progress are a code and parameters: each person reads them in their
// language, and what an agent wrote in them is the same in both.
func TestTheProgressMessageReadsInTheLanguageOfThePerson(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	checkout := table("41-checkout", "Checkout", tables.StateInReview, false)
	checkout.PRNumber = 41
	r.tables.ts = []tables.Table{table("38-login", "Login", tables.StateInReview, true), checkout, table("42-busca", "Busca", tables.StateWorking, false)}
	r.reqs["41-checkout"] = []chain.Request{{ID: 7, Level: chain.Level{Kind: chain.LevelPeople}, PR: &chain.PRRef{Number: 41}, Why: []string{"mexe em pagamentos"}}}
	m, _, err := r.svc.Compose(t.Context(), "loja")
	if err != nil {
		t.Fatal(err)
	}
	wantEn := "loja · 3 tables\n  ✅ 1 merged (Login)\n  🔧 1 in progress (Busca)\n  🙋 Checkout: waiting for you — approve PR #41 (mexe em pagamentos)\nNext: Checkout goes back to work when you approve"
	if got := i18n.Render(i18n.En, m.Msg); got != wantEn {
		t.Errorf("en:\n%s\nwant:\n%s", got, wantEn)
	}
	wantPt := "loja · 3 mesas\n  ✅ 1 mesclada (Login)\n  🔧 1 em andamento (Busca)\n  🙋 Checkout: espera você — aprovar PR #41 (mexe em pagamentos)\nPróximo: Checkout volta ao trabalho quando você aprovar"
	if got := i18n.Render(i18n.PtBR, m.Msg); got != wantPt {
		t.Errorf("pt-BR:\n%s\nwant:\n%s", got, wantPt)
	}
}

func TestTheProgressMessageGoesAtTheEndOfAMissionAndEveryTwoHoursWhileATableWorks(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.tables.ts = []tables.Table{table("42-login", "Login", tables.StateWorking, false)}
	ctx := t.Context()

	// the first look starts the 2 hours; nothing goes before they pass
	if err := r.svc.Tick(ctx); err != nil || r.out.count() != 0 {
		t.Fatalf("first tick: %d sent, %v", r.out.count(), err)
	}
	r.clk.Advance(time.Hour + 59*time.Minute)
	_ = r.svc.Tick(ctx)
	if r.out.count() != 0 {
		t.Fatalf("sent before the 2 hours: %d", r.out.count())
	}
	r.clk.Advance(time.Minute)
	_ = r.svc.Tick(ctx)
	if r.out.count() != 1 {
		t.Fatalf("not sent after the 2 hours: %d", r.out.count())
	}
	_ = r.svc.Tick(ctx)
	if r.out.count() != 1 {
		t.Fatalf("sent twice in the same period: %d", r.out.count())
	}
	// the end of a mission sends at once and starts the 2 hours again; missions
	// that end together are one message
	r.clk.Advance(2 * time.Minute)
	r.svc.MissionEnded(ctx, "loja")
	r.svc.MissionEnded(ctx, "loja")
	if r.out.count() != 2 {
		t.Fatalf("the end of a mission did not send: %d", r.out.count())
	}
	r.clk.Advance(time.Hour)
	_ = r.svc.Tick(ctx)
	if r.out.count() != 2 {
		t.Errorf("sent an hour after the end of a mission: %d", r.out.count())
	}
	// no table working: no periodic message
	r.tables.ts = []tables.Table{table("42-login", "Login", tables.StateInReview, false)}
	r.clk.Advance(3 * time.Hour)
	_ = r.svc.Tick(ctx)
	if r.out.count() != 2 {
		t.Errorf("sent with no table working: %d", r.out.count())
	}
	// no table at all: nothing to say
	r.tables.ts = nil
	r.clk.Advance(time.Hour)
	r.svc.MissionEnded(ctx, "loja")
	if r.out.count() != 2 || !strings.HasPrefix(r.out.sent[0], "u1: loja · 1 mesa\n") {
		t.Errorf("sent = %q", r.out.sent)
	}
}

func TestTheProgressMessageMasksAndLabelsWhatAgentsWrote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	svc, err := progress.New(progress.Options{Clock: r.clk, Tables: r.tables, Requests: r.reqs, Workspaces: fakeWorkspaces{"loja"}, Recipients: fakePeople{"u1"}, Sender: r.out,
		Mask: func(s string) string { return strings.ReplaceAll(s, "s3cr3t", "[segredo removido]") }})
	if err != nil {
		t.Fatal(err)
	}
	pr := table("41-checkout", "Checkout", tables.StateWorking, false)
	pr.Paused, pr.PausedReason = true, "limite\nde uso s3cr3t"
	r.tables.ts = []tables.Table{pr}
	r.reqs["41-checkout"] = []chain.Request{{ID: 7, Level: chain.Level{Kind: chain.LevelGrandMaster}, About: "rodar\nnpm install s3cr3t"}}
	m, _, _ := svc.Compose(t.Context(), "loja")
	if strings.Contains(text(m), "s3cr3t") || strings.Contains(text(m), "\nde uso") || !strings.Contains(text(m), "espera o Grão-Mestre — rodar npm install [segredo removido]") ||
		!strings.Contains(text(m), "pausada — limite de uso [segredo removido]") {
		t.Errorf("message:\n%s", text(m))
	}
}

// A secret is masked before the text is cut, a title is one safe line, and the
// message stays under the size the bot takes.
func TestTheProgressMessageMasksBeforeCuttingAndKeepsTheTitleToOneLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	secret := "ghp_" + strings.Repeat("a", 36)
	svc, err := progress.New(progress.Options{Clock: r.clk, Tables: r.tables, Requests: r.reqs, Workspaces: fakeWorkspaces{"loja"}, Recipients: fakePeople{"u1"}, Sender: r.out,
		Mask: func(s string) string { return strings.ReplaceAll(s, secret, "[segredo removido]") }})
	if err != nil {
		t.Fatal(err)
	}
	bad := table("41-checkout", "Check\nout "+secret+"\x1b[2J", tables.StateInReview, false)
	r.tables.ts = []tables.Table{bad}
	r.reqs["41-checkout"] = []chain.Request{{ID: 7, Level: chain.Level{Kind: chain.LevelPeople}, About: strings.Repeat("x", 150) + secret}}
	m, _, _ := svc.Compose(t.Context(), "loja")
	if strings.Contains(text(m), "ghp_") || strings.Contains(text(m), "\x1b") || strings.Contains(text(m), "Check\nout") {
		t.Errorf("message:\n%s", text(m))
	}
	var many []tables.Table
	for i := range 200 {
		many = append(many, table("t"+strings.Repeat("x", i), strings.Repeat("título ", 10), tables.StateWorking, true))
	}
	r.tables.ts = many
	if m, _, _ := svc.Compose(t.Context(), "loja"); len([]rune(text(m))) > 3600 {
		t.Errorf("the message has %d characters", len([]rune(text(m))))
	}
}

type flakySender struct {
	fail map[string]bool
	sent []string
}

func (f *flakySender) Send(_ context.Context, user, _ string, _ i18n.Msg) error {
	if f.fail[user] {
		return context.DeadlineExceeded
	}
	f.sent = append(f.sent, user)
	return nil
}

// A message nobody got does not use up the turn; one that reached some does.
func TestAMessageThatReachedNobodyIsTriedAgainAndOneThatReachedSomeIsNot(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	snd := &flakySender{fail: map[string]bool{"u1": true, "u2": true}}
	svc, err := progress.New(progress.Options{Clock: r.clk, Tables: r.tables, Requests: r.reqs, Workspaces: fakeWorkspaces{"loja"}, Recipients: fakePeople{"u1", "u2"}, Sender: snd})
	if err != nil {
		t.Fatal(err)
	}
	r.tables.ts = []tables.Table{table("42-login", "Login", tables.StateWorking, false)}
	if err := svc.Send(t.Context(), "loja"); err == nil {
		t.Fatal("no error for a message nobody got")
	}
	snd.fail["u2"] = false
	if err := svc.Send(t.Context(), "loja"); err == nil || len(snd.sent) != 1 {
		t.Fatalf("second try: %v, sent %v; want the failure of u1 told and u2 reached", err, snd.sent)
	}
	snd.fail["u1"] = false
	_ = svc.Send(t.Context(), "loja") // within the minute: the turn was used by the one that reached u2
	if len(snd.sent) != 1 {
		t.Errorf("sent again at once: %v", snd.sent)
	}
}

// A workspace that does not act gets no message at the end of a mission.
func TestNoMessageForAWorkspaceThatDoesNotAct(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.tables.ts = []tables.Table{table("42-login", "Login", tables.StateWorking, false)}
	r.svc.MissionEnded(t.Context(), "outro")
	if r.out.count() != 0 {
		t.Errorf("sent for a workspace that is not active: %d", r.out.count())
	}
}
