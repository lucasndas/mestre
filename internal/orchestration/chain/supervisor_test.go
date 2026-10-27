package chain_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// The criteria of the supervision of the chain
// (docs/orquestracao/supervisao-da-cadeia.md): the real Supervisor over the
// chain, the temporary table rules and the inbox of the tests.

func newSup(t *testing.T) *world {
	t.Helper()
	return newWorld(t, worldOpts{realSup: true, realTemp: true})
}

// summaries lists the summaries of the decisions the Grão-Mestre got.
func (w *world) summaries() []inbox.Item {
	w.t.Helper()
	var out []inbox.Item
	for _, it := range w.itemsOf(w.gm, inbox.KindSystem) {
		if strings.HasPrefix(it.Text, "Resumo das decisões dos Mestres de mesa") {
			out = append(out, it)
		}
	}
	return out
}

func (w *world) tick() {
	w.t.Helper()
	if err := w.supervisor.Tick(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// alertsOf lists what the people and the Grão-Mestre were told about queues.
func (w *world) queueAlertsToPeople() []string {
	w.alerts.mu.Lock()
	defer w.alerts.mu.Unlock()
	var out []string
	for _, a := range w.alerts.told {
		if strings.Contains(a, "Fila acumulando") {
			out = append(out, a)
		}
	}
	return out
}

func (w *world) queueAlertsToGM() []inbox.Item {
	w.t.Helper()
	var out []inbox.Item
	for _, it := range w.itemsOf(w.gm, inbox.KindSystem) {
		if strings.HasPrefix(it.Text, "Fila acumulando") {
			out = append(out, it)
		}
	}
	return out
}

func (w *world) pause(by chain.Actor, table, reason string) (bool, error) {
	return w.supervisor.PauseAutonomy(w.ctx, chain.PauseRequest{Workspace: w.slug, Table: table, Reason: reason, By: by})
}

func (w *world) resume(by chain.Actor, table, reason string) (bool, error) {
	return w.supervisor.ResumeAutonomy(w.ctx, chain.PauseRequest{Workspace: w.slug, Table: table, Reason: reason, By: by})
}

func (w *world) gmActor() chain.Actor {
	gm := w.get(w.gm.ID)
	return chain.Actor{Character: &gm}
}

// CA-SUPCAD-01
func TestTheGrandMasterGetsTheSummaryOfTheTableMastersEvery30MinutesWithoutWaitingOrADeadline(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev := w.recruit("Dev")
	table43, master43, dev43 := w.otherTable()
	w.cls.domains[dev.ID] = []string{"registry.npmjs.org"}

	// An empty period delivers nothing.
	w.clk.Advance(30 * time.Minute)
	w.tick()
	if got := w.summaries(); len(got) != 0 {
		t.Fatalf("a period without decisions delivered %d summaries", len(got))
	}

	a := w.shell(dev, "pip install x")
	b := w.shell(dev, "rm -r build")
	c := w.shell(dev43, "pip install y")
	w.decide(w.master, a, chain.AnswerApprove, "pacote comum")
	w.decide(w.master, b, chain.AnswerDeny, "apaga a pasta errada")
	w.decide(master43, c, chain.AnswerApprove, "pacote comum")
	// the Grão-Mestre decides one too: it is not in the summary of the Mestres de mesa
	g := w.shell(dev, "curl https://exemplo.io")
	w.decide(w.get(w.gm.ID), g, chain.AnswerApprove, "dominio conhecido")

	w.clk.Advance(29*time.Minute + 59*time.Second)
	w.tick()
	if got := w.summaries(); len(got) != 0 {
		t.Fatalf("the summary came out before 30 minutes: %d", len(got))
	}
	w.clk.Advance(time.Second)
	w.tick()
	got := w.summaries()
	if len(got) != 1 {
		t.Fatalf("the Grand Master got %d summaries, want 1", len(got))
	}
	it := got[0]
	i42, i43 := strings.Index(it.Text, "Mesa "+w.table.ID), strings.Index(it.Text, "Mesa "+table43.ID)
	if i42 < 0 || i43 < 0 || i42 > i43 {
		t.Errorf("the summary is not by table:\n%s", it.Text)
	}
	for _, want := range []string{fmt.Sprintf("#%d", a), fmt.Sprintf("#%d", b), fmt.Sprintf("#%d", c), "apaga a pasta errada", "pip install x", "risco"} {
		if !strings.Contains(it.Text, want) {
			t.Errorf("the summary lacks %q:\n%s", want, it.Text)
		}
	}
	if strings.Contains(it.Text, fmt.Sprintf("#%d ·", g)) {
		t.Errorf("a decision of the Grand Master is in the summary of the table masters:\n%s", it.Text)
	}
	if it.Kind != inbox.KindSystem || it.Refs.Approval != 0 {
		t.Errorf("the summary is %s with approval ref %d; want a system item that asks nothing", it.Kind, it.Refs.Approval)
	}
	if reason := w.activeWait(w.get(w.gm.ID)); reason != "" {
		t.Errorf("the summary opened a wait: %q", reason)
	}
	for _, r := range []int64{a, b, c} {
		if w.request(r).Deadline.After(time.Time{}) {
			t.Errorf("request #%d has a deadline after being decided", r)
		}
	}
	// Nothing new: nothing more is delivered.
	w.clk.Advance(30 * time.Minute)
	w.tick()
	if got := w.summaries(); len(got) != 1 {
		t.Errorf("a second summary without decisions: %d", len(got))
	}
}

// CA-SUPCAD-02
func TestPausingTheAutonomyOfATableSendsItsMedioUpAndSuspendsTheRulesOfTheTableMaster(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	for _, d := range []characters.Character{dev1, dev2} {
		w.cls.domains[d.ID] = []string{"registry.npmjs.org", "pypi.org"}
	}
	// a medio rule of the table master
	first := w.shell(dev1, "curl https://pypi.org/x")
	w.approvePattern(first, "curl *", 2*time.Hour)
	rule := w.rules()[0]
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict != classes.Allow || d.Decree != rule.ID {
		t.Fatalf("the rule does not free the command before the pause: %+v", d)
	}
	open := w.shell(dev1, "pip install x")
	if w.level(open) != chain.LevelMesa {
		t.Fatalf("the medio request is at %s", w.level(open))
	}

	changed, err := w.pause(w.gmActor(), w.table.ID, "muitas falhas")
	if err != nil || !changed {
		t.Fatalf("pause = %v, %v", changed, err)
	}
	if paused, _ := w.supervisor.AutonomyPaused(w.ctx, w.slug, w.table.ID); !paused {
		t.Fatal("the table is not paused")
	}
	if r := w.request(open); r.ID != open || r.State != chain.Open || r.Level.Kind != chain.LevelGrandMaster || r.Level.Agent != w.gm.ID {
		t.Errorf("the open medio request is %s at %+v; want the same id at the Grand Master", r.State, r.Level)
	}
	if items := w.approvalItems(w.gm); len(items) != 1 || items[0].Refs.Approval != open {
		t.Errorf("the Grand Master has %d approval items; want the one of #%d", len(items), open)
	}
	if lvl := w.level(w.shell(dev2, "pip install y")); lvl != chain.LevelGrandMaster {
		t.Errorf("the next medio was born at %s, want the Grand Master", lvl)
	}
	if lvl := w.level(w.shell(dev2, "npm install lodash")); lvl != chain.LevelMesa {
		t.Errorf("a baixo was born at %s, want the table master", lvl)
	}
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict == classes.Allow {
		t.Errorf("the medio rule of the table master freed %+v during the pause", d)
	}

	if changed, err := w.resume(w.gmActor(), w.table.ID, "voltou ao normal"); err != nil || !changed {
		t.Fatalf("resume = %v, %v", changed, err)
	}
	if lvl := w.level(w.shell(dev2, "pip install z")); lvl != chain.LevelMesa {
		t.Errorf("after the resume the medio was born at %s, want the table master", lvl)
	}
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict != classes.Allow || d.Decree != rule.ID {
		t.Errorf("after the resume the rule does not free the command: %+v", d)
	}
	// resuming what is not paused succeeds without effect
	if changed, err := w.resume(w.gmActor(), w.table.ID, "de novo"); err != nil || changed {
		t.Errorf("resume of a table that is not paused = %v, %v", changed, err)
	}
}

// CA-SUPCAD-03
func TestARequestThatStaysAtTheGrandMasterPastTwiceItsDeadlineWarnsTheAdministratorsAndTheOwner(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "curl https://exemplo.io")
	if w.level(id) != chain.LevelGrandMaster {
		t.Fatalf("the request is at %s", w.level(id))
	}
	// asking back holds the request at the level without its deadline
	if _, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerAskBack, Instruction: "por que este domínio?", Reason: "falta contexto"}); err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(19 * time.Minute)
	w.tick()
	if got := w.queueAlertsToPeople(); len(got) != 0 {
		t.Fatalf("warned at 19 minutes: %v", got)
	}
	w.clk.Advance(2 * time.Minute)
	w.tick()
	w.tick()
	got := w.queueAlertsToPeople()
	if len(got) != 1 || !strings.Contains(got[0], "Administradores e Senhor da Guilda") || !strings.Contains(got[0], "Grão-Mestre") || !strings.Contains(got[0], "21 min") {
		t.Fatalf("alerts to the people = %v", got)
	}
	if len(w.queueAlertsToGM()) != 0 {
		t.Error("the Grand Master was warned of his own queue")
	}
}

// CA-SUPCAD-04
func TestTheQueueMetricsGiveOpenOldestAndMeanWaitPerApproverFromTheLevelsTheRequestsWentThrough(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev := w.recruit("Dev")
	quick := w.shell(dev, "pip install quick") // decided by the table master after a minute
	slow := w.shell(dev, "pip install slow")   // climbs to the Grand Master, decided there
	w.clk.Advance(time.Minute)
	w.decide(w.master, quick, chain.AnswerApprove, "ok")
	w.clk.Advance(time.Minute) // 2 min since #slow: it climbs
	if w.level(slow) != chain.LevelGrandMaster {
		t.Fatalf("the slow request is at %s", w.level(slow))
	}
	w.clk.Advance(3 * time.Minute)
	w.decide(w.get(w.gm.ID), slow, chain.AnswerApprove, "ok")
	w.shell(dev, "pip install pending")
	w.clk.Advance(time.Minute)

	ms, err := w.supervisor.Metrics(w.ctx, w.slug)
	if err != nil {
		t.Fatal(err)
	}
	find := func(l chain.LevelKind) chain.QueueMetric {
		for _, m := range ms {
			if m.Level == l {
				return m
			}
		}
		return chain.QueueMetric{Level: l}
	}
	mesa, gm := find(chain.LevelMesa), find(chain.LevelGrandMaster)
	if mesa.Table != w.table.ID || mesa.Open != 1 || mesa.Oldest != time.Minute || mesa.Waited != 2 || mesa.AvgWait != 90*time.Second {
		t.Errorf("table master = %+v; want 1 open for 1m and 2 waits averaging 1m30s", mesa)
	}
	if gm.Open != 0 || gm.Waited != 1 || gm.AvgWait != 3*time.Minute {
		t.Errorf("Grand Master = %+v; want 1 wait of 3m and nothing open", gm)
	}
	if p := find(chain.LevelPeople); p.Open != 0 || p.Waited != 0 {
		t.Errorf("people = %+v", p)
	}

	// the mean is over the last 24 hours
	w.clk.Advance(25 * time.Hour)
	ms, _ = w.supervisor.Metrics(w.ctx, w.slug)
	for _, m := range ms {
		if (m.Level == chain.LevelGrandMaster || m.Level == chain.LevelMesa) && m.Waited != 0 {
			t.Errorf("a wait that ended more than 24 hours ago is still in the mean: %+v", m)
		}
	}
}

// The sample of the decisions of the table masters for the digest of the Owner
// (M5 delivers the digest): drawn with an injected source, the same for the same
// seed, all of them when there are few.
func TestTheSampleOfTheDecisionsOfTheTableMastersUsesTheInjectedSourceOfRandomness(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev := w.recruit("Dev")
	start := w.clk.Now()
	for i := range 6 {
		w.decide(w.master, w.shell(dev, fmt.Sprintf("pip install s%d", i)), chain.AnswerApprove, "ok")
	}
	var src chain.DecisionSource = w.supervisor
	until := w.clk.Now().Add(time.Minute)
	draw := func(seed uint64, n int) []int64 {
		t.Helper()
		got, err := src.Sample(w.ctx, w.slug, chain.LevelMesa, start, until, n, rand.New(rand.NewPCG(seed, 1)))
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, d := range got {
			ids = append(ids, d.Request)
		}
		return ids
	}
	a, b := draw(7, 3), draw(7, 3)
	if len(a) != 3 || !slices.Equal(a, b) || !slices.IsSorted(a) {
		t.Errorf("sample = %v then %v; want the same 3, in order", a, b)
	}
	if c := draw(8, 3); slices.Equal(a, c) {
		t.Errorf("another seed drew the same %v", c)
	}
	if all := draw(7, 20); len(all) != 6 {
		t.Errorf("a sample bigger than the decisions gave %d, want all 6", len(all))
	}
	if gm, _ := src.Decisions(w.ctx, w.slug, chain.LevelGrandMaster, start, until); len(gm) != 0 {
		t.Errorf("decisions of the Grand Master = %d", len(gm))
	}
}

// The limits of the sections the supervision reads are the file's
// (docs/aprovacoes/arquivo-de-politica.md).
func TestTheSummaryIntervalAndTheQueueLimitsComeFromTheSectionAndAreChecked(t *testing.T) {
	t.Parallel()
	s, err := chain.ParseSection([]byte("resumo_mesas: 10m\nalerta_fila: { abertos: 5, fator: 3 }\n"), 0)
	if err != nil || s.SummaryEvery != 10*time.Minute || s.QueueAlertOpen != 5 || s.QueueAlertFactor != 3 {
		t.Fatalf("section = %+v, %v", s, err)
	}
	d, err := chain.ParseSection(nil, 0)
	if err != nil || d.SummaryEvery != 30*time.Minute || d.QueueAlertOpen != 10 || d.QueueAlertFactor != 2 {
		t.Fatalf("default = %+v, %v", d, err)
	}
	for item, text := range map[string]string{
		"cadeia.alerta_fila.abertos":  "alerta_fila: { abertos: 0 }\n",
		"cadeia.alerta_fila.abertos ": "alerta_fila: { abertos: 1001 }\n",
		"cadeia.alerta_fila.fator":    "alerta_fila: { fator: 11 }\n",
		"cadeia.alerta_fila.fator ":   "alerta_fila: { fator: 0 }\n",
		"cadeia.resumo_mesas":         "resumo_mesas: soon\n",
	} {
		_, err := chain.ParseSection([]byte(text), 0)
		var se *chain.SectionError
		if !errorsAs(err, &se) || se.Item != strings.TrimSpace(item) {
			t.Errorf("%q: error = %v; want the item %s", text, err, item)
		}
	}
}

func errorsAs(err error, target **chain.SectionError) bool {
	if se, ok := err.(*chain.SectionError); ok {
		*target = se
		return true
	}
	return false
}

// Part of CA-SUPCAD-05 (it closes in internal/telegram, with the button
// Pausar autonomia of the queue warning over the real supervision; here the
// engine handlers and the interface of the supervision with a double for the
// person).
func TestThePauseIsAuditedWithWhoAndWhyAndTheLevelRulesHoldForPausingAndResuming(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	table43, master43, _ := w.otherTable()
	call := func(by characters.Character, cmd string, flags map[string]any) localchan.Response {
		t.Helper()
		// the CLI resolves two words: pause or resume is the argument
		args := localchan.TextArgs("pause")
		if strings.HasSuffix(cmd, " resume") {
			cmd, args = strings.TrimSuffix(cmd, " resume"), localchan.TextArgs("resume")
		} else {
			cmd = strings.TrimSuffix(cmd, " pause")
		}
		return w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: cmd, Args: args, Flags: flags}, Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	}
	// the Grand Master by the command
	resp := call(w.gm, "approval autonomy pause", map[string]any{"table": w.table.ID, "reason": "muitas falhas"})
	if !resp.OK {
		t.Fatalf("pause by the Grand Master: %+v", resp.Error)
	}
	log, err := w.supervisor.PauseAudit(w.ctx, w.slug, w.table.ID)
	if err != nil || len(log) != 1 || log[0].Action != "pause" || log[0].By != w.gm.ID || log[0].Reason != "muitas falhas" || log[0].At.IsZero() {
		t.Fatalf("audit = %+v, %v", log, err)
	}
	if p, ok, _ := w.supervisor.PauseOf(w.ctx, w.slug, w.table.ID); !ok || p.By != w.gm.ID || p.Reason != "muitas falhas" {
		t.Errorf("pause = %+v, %v", p, ok)
	}
	if items := w.itemsOf(w.master, inbox.KindSystem); !slices.ContainsFunc(items, func(i inbox.Item) bool { return strings.Contains(i.Text, "pausada") }) {
		t.Error("the table master was not told")
	}
	// the table master does not pause, with level_denied
	if r := call(w.master, "approval autonomy pause", map[string]any{"table": table43.ID, "reason": "x"}); r.OK || r.Error.Code != "level_denied" || r.ExitCode() != localchan.ExitDenied {
		t.Errorf("pause by a table master = %+v", r)
	}
	if r := call(master43, "approval autonomy pause", map[string]any{"table": table43.ID, "reason": "x"}); r.OK || r.Error.Code != "level_denied" {
		t.Errorf("pause of its own table by its master = %+v", r)
	}
	if r := call(w.gm, "approval autonomy pause", map[string]any{"table": table43.ID}); r.OK || r.Error.Code != localchan.CodeUsage {
		t.Errorf("pause without reason = %+v", r)
	}
	if r := call(w.gm, "approval autonomy pause", map[string]any{"table": "99-nada", "reason": "x"}); r.OK || r.Error.Code != "not_found" {
		t.Errorf("pause of a table that does not exist = %+v", r)
	}
	// an Approver pauses (the button) and the Grand Master cannot resume it
	if changed, err := w.pause(person(characters.LevelApprover), table43.ID, "fila grande"); err != nil || !changed {
		t.Fatalf("pause by an Approver = %v, %v", changed, err)
	}
	if r := call(w.gm, "approval autonomy resume", map[string]any{"table": table43.ID, "reason": "voltar"}); r.OK || r.Error.Code != "level_denied" {
		t.Errorf("resume by the Grand Master of the pause of a person = %+v", r)
	}
	if paused, _ := w.supervisor.AutonomyPaused(w.ctx, w.slug, table43.ID); !paused {
		t.Error("the refused resume changed the pause")
	}
	if _, err := w.resume(person(characters.LevelObserver), table43.ID, "x"); err == nil {
		t.Error("an Observer resumed")
	}
	if changed, err := w.resume(person(characters.LevelApprover), table43.ID, "ok"); err != nil || !changed {
		t.Errorf("resume by an Approver = %v, %v", changed, err)
	}
	// the pause of the Grand Master is resumed by him
	if r := call(w.gm, "approval autonomy resume", map[string]any{"table": w.table.ID, "reason": "ok"}); !r.OK {
		t.Errorf("resume by the Grand Master of its own pause: %+v", r.Error)
	}
	log, _ = w.supervisor.PauseAudit(w.ctx, w.slug, w.table.ID)
	if len(log) != 2 || log[1].Action != "resume" || log[1].Reason != "ok" {
		t.Errorf("audit after the resume = %+v", log)
	}
	// the status of the table says it
	if _, err := w.pause(w.gmActor(), w.table.ID, "de novo"); err != nil {
		t.Fatal(err)
	}
	st := w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "table status", Args: localchan.TextArgs(w.table.ID)}, Caller: localchan.Caller{Identified: true, AgentID: w.gm.ID}})
	if !st.OK || !strings.Contains(string(st.Data), "Autonomia do Mestre da mesa pausada") {
		t.Errorf("table status = %s", st.Data)
	}
}

// CA-SUPCAD-03
func TestAnAccumulatingQueueWarnsWhoIsAboveOnceUntilItGoesBackBelowTheLimits(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev := w.recruit("Dev")
	open := func(from, to int) []int64 {
		var ids []int64
		for i := from; i < to; i++ {
			ids = append(ids, w.shell(dev, fmt.Sprintf("pip install p%d", i)))
		}
		return ids
	}
	ids := open(0, 10)
	w.clk.Advance(time.Minute)
	w.tick()
	if got := w.queueAlertsToGM(); len(got) != 0 {
		t.Fatalf("10 open requests warned the Grand Master: %d", len(got))
	}
	ids = append(ids, open(10, 11)...)
	w.tick()
	w.tick() // one at a time
	alerts := w.queueAlertsToGM()
	if len(alerts) != 1 {
		t.Fatalf("the Grand Master got %d alerts, want 1", len(alerts))
	}
	for _, want := range []string{w.table.ID, "11", "1 min"} {
		if !strings.Contains(alerts[0].Text, want) {
			t.Errorf("the alert lacks %q:\n%s", want, alerts[0].Text)
		}
	}
	if len(w.queueAlertsToPeople()) != 0 {
		t.Error("the people were warned of a queue of a table master")
	}
	// the people who may pause the autonomy of the table get the warning too, with
	// the button (the Telegram), once
	if w.queuers.count() != 1 || w.queuers.told[0].Table != w.table.ID || w.queuers.told[0].Workspace != w.slug || !strings.Contains(w.queuers.told[0].Text, "11") {
		t.Errorf("warnings with the button: %+v", w.queuers.told)
	}
	// back below the limits, and over them again
	w.decide(w.master, ids[0], chain.AnswerApprove, "ok")
	w.tick()
	if got := w.queueAlertsToGM(); len(got) != 1 {
		t.Fatalf("10 open again, alerts = %d", len(got))
	}
	open(20, 21)
	w.tick()
	if got := w.queueAlertsToGM(); len(got) != 2 {
		t.Errorf("after going below and over again the Grand Master has %d alerts, want 2", len(got))
	}
}

func person(level characters.UserLevel) chain.Actor {
	return chain.Actor{User: "u-" + string(level), UserLevel: level}
}

// A panel resume that refuses the pause of the Grão-Mestre checks it under the lock that
// reads the pause: the Approver does not resume it, the Grão-Mestre does, and without the
// option the Approver still can (the Telegram path).
func TestAResumeThatRefusesTheGrandMastersPauseLeavesItInPlace(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	approver := chain.Actor{User: "u-approver", UserLevel: characters.LevelApprover}
	if _, err := w.pause(w.gmActor(), w.table.ID, "muitas falhas"); err != nil {
		t.Fatal(err)
	}
	req := chain.PauseRequest{Workspace: w.slug, Table: w.table.ID, Reason: "voltem", By: approver, RefuseCharacterPause: true}
	if changed, err := w.supervisor.ResumeAutonomy(w.ctx, req); !errors.Is(err, chain.ErrCharacterPause) || changed {
		t.Fatalf("resume = %v, %v, want ErrCharacterPause", changed, err)
	}
	if paused, _ := w.supervisor.AutonomyPaused(w.ctx, w.slug, w.table.ID); !paused {
		t.Fatal("the refused resume ended the pause")
	}
	req.By, req.Reason = chain.Actor{Character: ptr(w.get(w.gm.ID))}, "ok"
	if changed, err := w.supervisor.ResumeAutonomy(w.ctx, req); err != nil || !changed {
		t.Fatalf("the Grão-Mestre resume = %v, %v", changed, err)
	}
	if _, err := w.pause(w.gmActor(), w.table.ID, "de novo"); err != nil {
		t.Fatal(err)
	}
	if changed, err := w.resume(approver, w.table.ID, "telegram"); err != nil || !changed {
		t.Fatalf("an Approver resume without the option = %v, %v", changed, err)
	}
}

func ptr[T any](v T) *T { return &v }
