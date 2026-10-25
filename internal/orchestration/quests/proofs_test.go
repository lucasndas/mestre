package quests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// proofAll is the class that asks for a proof of every criterion.
type proofAll struct{}

func (proofAll) DoneRequires(context.Context, string, string, string) (classes.DoneRequires, error) {
	return classes.DoneRequires{Proof: classes.ProofAll}, nil
}

// issueOf makes the issue the Master of the table would have made.
func (w *world) issueOf(body string) int {
	w.t.Helper()
	w.hub.mu.Lock()
	defer w.hub.mu.Unlock()
	return w.hub.make(42, "Login", body)
}

func (w *world) issueBody(n int) string { return w.hub.get(n).Body }

func (w *world) done(name string, flags map[string]any, summary string) localchan.Response {
	w.t.Helper()
	return w.call(w.char(name).ID, "done", flags, summary)
}

func proofQuest(issue int) string {
	return fmt.Sprintf(`
name: Prova
issue: %d
steps:
  - {id: dev, sheet: Dev Claude}
  - {id: qa, sheet: QA Antigravity, after: [dev]}
`, issue)
}

// CA-PROVA-02
func TestAClassThatAsksForProofRefusesTheDoneThatLacksOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{done: proofAll{}})
	n := w.issueOf("- [ ] C1 primeiro\n- [ ] C2 segundo\n")
	q := w.run(w.create(fmt.Sprintf("name: Prova\nissue: %d\nsteps:\n  - {id: qa, sheet: QA Antigravity}\n", n)))

	r := w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=t"}}, "x")
	if r.OK || r.Error.Code != "proof_missing" || r.ExitCode() != localchan.ExitConflict || !strings.Contains(r.Error.Message, "C2") || strings.Contains(r.Error.Message, "C1") {
		t.Fatalf("done with C1 only = %+v", r)
	}
	w.wantStates(q, map[string]quests.StepState{"qa": quests.StepRunning})
	r = w.done("QA Antigravity 1", map[string]any{"proof": []string{"C2=u"}}, "y")
	if !r.OK {
		t.Fatalf("done with C2 after C1 = %+v", r.Error)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s, want completed", got.State)
	}
	if !strings.Contains(w.issueBody(n), "- [x] C1 primeiro") || !strings.Contains(w.issueBody(n), "- [x] C2 segundo") {
		t.Errorf("the issue:\n%s", w.issueBody(n))
	}
}

// Part of CA-PROVA-06 (the story with the three returns, the other print in the
// middle and the event is in the full suite): the same print twice in a row
// pauses the quest before the returns run out, and the return is not made.
func TestTheSamePlaceTwiceInARowPausesTheQuestAndTellsTheMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create("same_failure: 2\nmax_returns: 5\nname: Revisão\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	returnWith := func(where string) quests.DoneResult {
		r := w.done("Revisor Codex 1", map[string]any{"return": "sem teste", "cause": "test", "where": []string{where}}, "reprovado")
		var res quests.DoneResult
		if err := json.Unmarshal(r.Data, &res); err != nil || !r.OK {
			t.Fatalf("return = %+v (%v)", r, err)
		}
		return res
	}
	if res := returnWith("src/login.go:40"); res.ReturnedTo == nil || res.QuestState != "running" {
		t.Fatalf("the first = %+v, want it back to the Dev", res)
	}
	w.machines.Report(w.ctx, w.char("Revisor Codex 1").ID, "idle")
	w.finish("Dev Claude 1", "v2", map[string]string{"a.go": "2"}, "")
	if res := returnWith("./src/login.go:40:7"); res.ReturnedTo != nil || res.QuestState != "paused" {
		t.Fatalf("the second = %+v, want paused and not returned", res)
	}
	if got := w.quest(q.ID); got.PausedReason != "mesma falha repetida" {
		t.Errorf("paused for %q", got.PausedReason)
	}
	found := false
	for _, d := range w.machines.Delivered(w.master) {
		found = found || strings.Contains(d, "return_limit") && strings.Contains(d, "mesma falha repetida") && strings.Contains(d, "src/login.go:40")
	}
	if !found {
		t.Error("the Master did not get the event return_limit with the place")
	}
}

// Part of CA-PROVA-06 and CA-PROVA-07 (the story with three returns is in the
// full suite): another place in the middle starts the count again, and returns
// with no place never pause for the same failure.
func TestAnotherPlaceInTheMiddleOrNoPlaceNeverPausesForTheSameFailure(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create("same_failure: 2\nmax_returns: 9\nname: Revisão\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	for i, flags := range []map[string]any{
		{"cause": "test", "where": []string{"src/login.go:40"}}, {"cause": "test", "where": []string{"src/login.go:52"}}, {"cause": "test", "where": []string{"src/login.go:40"}},
		{}, {"cause": "test"}, {"cause": "test"}, {},
	} {
		flags["return"] = "ainda não"
		r := w.done("Revisor Codex 1", flags, "reprovado")
		var res quests.DoneResult
		if err := json.Unmarshal(r.Data, &res); err != nil || res.ReturnedTo == nil || res.QuestState != "running" {
			t.Fatalf("return %d = %+v (%v), want it back to the Dev", i+1, res, err)
		}
		w.machines.Report(w.ctx, w.char("Revisor Codex 1").ID, "idle")
		w.finish("Dev Claude 1", fmt.Sprintf("v%d", i+2), map[string]string{"a.go": fmt.Sprint(i + 2)}, "")
	}
	if got := w.quest(q.ID); got.State != quests.Running {
		t.Errorf("the quest is %s", got.State)
	}
}

// CA-PROVA-08
func TestAQuestThatReturnsHasAReviewScrollThatTheSecondPassReads(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.recruit("Dev Claude", "Dev Claude 1")
	w.recruit("Revisor Codex", "Revisor Codex 1")
	q := w.run(w.create(`
name: Revisão
max_returns: 3
steps:
  - {id: dev,    character: Dev Claude 1, instructions: "Implemente"}
  - {id: review, character: Revisor Codex 1, after: [dev], can_return_to: dev}
`))
	note, text, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: "Revisão: Revisão"})
	if err != nil {
		t.Fatalf("the review scroll: %v", err)
	}
	// connected to the Reviewer and to the Dev, and to nobody else
	outsider := w.recruit("QA Antigravity", "QA Antigravity 7")
	for name, want := range map[string]bool{"Dev Claude 1": true, "Revisor Codex 1": true, outsider.Name: false} {
		if ok, err := w.orc.Connections.CanSeeScroll(w.ctx, w.char(name).ID, note.Key); err != nil || ok != want {
			t.Errorf("%s sees the review scroll = %v (%v), want %v", name, ok, err, want)
		}
	}
	if !strings.Contains(text, "| ID | Etapa dona | Tipo | Onde | Achado | Estado |") {
		t.Errorf("the review scroll:\n%s", text)
	}
	// the table is born empty: no row of data after the header
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "| R") {
			t.Errorf("the review scroll is born with a row of data: %q", line)
		}
	}
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	if item := w.itemOf("Revisor Codex 1"); strings.Contains(item, "Pergaminho de revisão") {
		t.Errorf("the first pass already has the review scroll:\n%s", item)
	}
	// the reviewer writes a finding of the dev and hands the work back
	_, _, err = w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: "Revisão: Revisão"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Scrolls.AppendSection(w.ctx, w.slug, w.table.ID, "Revisão: Revisão", "| R1 | dev | Bug | `src/login.go:40` | aceita senha vazia | aberto |\n| R2 | review | Gap | `x.go:1` | outro | aberto |"); err != nil {
		t.Fatal(err)
	}
	w.finish("Revisor Codex 1", "reprovado", nil, "aceita senha vazia")
	devItem := w.itemOf("Dev Claude 1")
	if !strings.Contains(devItem, "R1") || strings.Contains(devItem, "R2") || !strings.Contains(devItem, "Achados da revisão que são seus") {
		t.Errorf("the dev did not get exactly its findings:\n%s", devItem)
	}
	w.finish("Dev Claude 1", "v2", map[string]string{"a.go": "2"}, "")
	item := w.itemOf("Revisor Codex 1")
	review := strings.Index(item, "Pergaminho de revisão")
	order := strings.Index(item, "confira cada achado")
	content := strings.Index(item, "aceita senha vazia")
	if review < 0 || content < 0 || order < 0 || !strings.Contains(item, "aberto") || order < review || order > content {
		t.Errorf("the second pass of the reviewer does not carry the review scroll with the order to check the findings:\n%s", item)
	}
	// it starts by the scroll: every other section of the item comes after the content of the scroll
	for _, later := range []string{"Resultado das etapas anteriores", "Issue da missão", "Pergaminho da missão", "Quando terminar", "Instruções:"} {
		if i := strings.Index(item, later); i >= 0 && i < content {
			t.Errorf("%q comes before the review scroll in the second pass:\n%s", later, item)
		}
	}
	if head := item[:review]; strings.Count(strings.TrimSpace(head), "\n") > 1 {
		t.Errorf("something other than the title comes before the review scroll:\n%s", item)
	}
	_ = q
}

// CA-PROVA-11
func TestTheCriteriaAreFrozenForTheRoundAndTheNextRoundReadsThemAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n- [ ] C2 dois\n")
	q := w.run(w.create("name: Uma\nissue: " + fmt.Sprint(n) + "\nsteps:\n  - {id: dev, sheet: Dev Claude}\n"))
	w.hub.mu.Lock()
	w.hub.issues[n].Body += "- [ ] C3 três\n"
	w.hub.mu.Unlock()
	crit, _ := w.orc.Quests.Store().Criteria(w.ctx, q.ID)
	if len(crit) != 2 {
		t.Fatalf("criteria = %+v, want C1 and C2 for the round", crit)
	}
	if r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C3=x"}}, "ok"); r.OK || r.Error.Code != "not_found" {
		t.Fatalf("a proof of the criterion added meanwhile = %+v", r)
	}
	w.finish("Dev Claude 1", "feito", nil, "")
	if contains(w.hub.calls, "issue") {
		t.Errorf("an issue was made: %v", w.hub.calls)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s", got.State)
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err != nil {
		t.Fatal(err)
	}
	crit, _ = w.orc.Quests.Store().Criteria(w.ctx, q.ID)
	if len(crit) != 3 {
		t.Errorf("the next round has %+v, want C1 to C3", crit)
	}
}

// CA-PROVA-12, the half of the done: a proof does not go with a return (and the cause, the places
// and the shape of a proof are checked with it, CA-PROVA-14 in part)
func TestProofDoesNotGoWithReturnAndTheCauseMustBeOneOfTheList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n")
	w.run(w.create("name: Rev\nissue: " + fmt.Sprint(n) + "\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", nil, "")
	for name, flags := range map[string]map[string]any{
		"proof with return":    {"return": "y", "proof": []string{"C1=t"}},
		"unknown cause":        {"return": "y", "cause": "urgente"},
		"cause with no return": {"cause": "test"},
		"where with no return": {"where": []string{"a.go:1"}},
		"four places":          {"return": "y", "where": []string{"a:1", "b:1", "c:1", "d:1"}},
		"proof with no equal":  {"proof": []string{"C1"}},
	} {
		r := w.done("Revisor Codex 1", flags, "x")
		if r.OK || r.ExitCode() != localchan.ExitUsage {
			t.Errorf("%s = %+v, want usage", name, r)
		}
	}
	if r := w.done("Revisor Codex 1", map[string]any{"proof": []string{"C1=" + strings.Repeat("x", quests.MaxProof+1)}}, "x"); r.OK || r.Error.Code != "too_large" || r.ExitCode() != localchan.ExitLimit {
		t.Errorf("a proof of 501 characters = %+v, want too_large", r)
	}
	// a task with no quest takes no proof
	if r := w.call(w.master.ID, "done", map[string]any{"proof": []string{"C1=t"}}, "x"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("a proof of a character with no step = %+v", r)
	}
}

// CA-PROVA-15, the half of the print and of the quest with no criteria
func TestThePlacesOfAFailureAreNormalizedAndAnIssueWithoutCriteriaRunsWithoutThem(t *testing.T) {
	t.Parallel()
	a, pa := quests.Fingerprint("review", "test", []string{"./a.go:12:5"}, "")
	b, pb := quests.Fingerprint("review", "test", []string{"a.go:12", "a.go:12"}, "")
	if a == "" || a != b || len(pa) != 1 || len(pb) != 1 {
		t.Errorf("prints %q %v and %q %v, want the same", a, pa, b, pb)
	}
	if c, _ := quests.Fingerprint("review", "test", []string{"a.go:13"}, ""); c == a {
		t.Error("another line has the same print")
	}
	if c, _ := quests.Fingerprint("qa", "test", []string{"a.go:12"}, ""); c == a {
		t.Error("another step has the same print")
	}
	// on Windows a place under the worktree is the same place as the relative one,
	// whatever the separators, the case of the path of the worktree or its last separator
	rel, relPlaces := quests.Fingerprint("review", "test", []string{"src/a.go:3"}, "")
	for name, c := range map[string]struct{ where, worktree string }{
		"backslashes":           {`C:\wt\src\a.go:3`, `C:\wt`},
		"forward slashes":       {`C:/wt/src/a.go:3`, `C:/wt`},
		"mixed":                 {`C:\wt/src\a.go:3:9`, `C:\wt\`},
		"another case":          {`c:\WT\src\a.go:3`, `C:\wt`},
		"relative with a drive": {`src\a.go:3`, `C:\wt`},
		"network share":         {`\\srv\Share\wt\src\a.go:3`, `\\SRV\share\wt`},
	} {
		d, places := quests.Fingerprint("review", "test", []string{c.where}, c.worktree)
		if d != rel || len(places) != 1 || places[0] != relPlaces[0] || places[0] != "src/a.go:3" {
			t.Errorf("%s: print %q places %v, want %q %v", name, d, places, rel, relPlaces)
		}
	}
	// the case of a Linux path is a difference, and a file outside the worktree stays whole
	if d, _ := quests.Fingerprint("review", "test", []string{"/Home/x/wt/src/a.go:3"}, "/home/x/wt"); d == rel {
		t.Error("the case of a path of a Unix worktree was ignored")
	}
	if _, places := quests.Fingerprint("review", "test", []string{`D:\other\a.go:3`}, `C:\wt`); len(places) != 1 || places[0] != "D:/other/a.go:3" {
		t.Errorf("a place outside the worktree = %v", places)
	}
	if e, _ := quests.Fingerprint("review", "test", nil, ""); e != "" {
		t.Errorf("no place has the print %q", e)
	}

	w := newWorld(t, worldOpts{done: proofAll{}})
	n := w.issueOf("Só texto, sem itens.\n")
	q := w.run(w.create(proofQuest(n)))
	if crit, _ := w.orc.Quests.Store().Criteria(w.ctx, q.ID); len(crit) != 0 {
		t.Fatalf("criteria = %+v", crit)
	}
	// the Master of the table is told to warn the Grand Master, once, when the quest runs
	if !w.told(w.master, "não tem nenhum item") || !w.told(w.master, "Avise o Grão-Mestre") || !w.told(w.master, fmt.Sprintf("#%d", n)) {
		t.Errorf("the Master of the table was not told to warn the Grand Master:\n%s", w.systemItemsOf(w.master))
	}
	w.finish("Dev Claude 1", "feito", nil, "")
	w.finish("QA Antigravity 1", "ok", nil, "") // proof: all asks nothing of a quest with no criteria
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s", got.State)
	}

	// an issue with criteria does not make the Master warn anyone
	w2 := newWorld(t, worldOpts{done: proofAll{}})
	w2.run(w2.create(proofQuest(w2.issueOf(criteriaBody))))
	if w2.told(w2.master, "Avise o Grão-Mestre") {
		t.Error("the Master was told to warn the Grand Master of an issue with criteria")
	}
}

// systemItemsOf is the text of the items of a character, for a failure message.
func (w *world) systemItemsOf(c characters.Character) string {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Text + "\n")
	}
	return b.String()
}

// CA-PROVA-15, the half of the outbox: a done with a proof of a criterion that
// does not exist, taken in by the Citadel after waiting in the outbox of a
// Tower, is refused, and the refusal goes to the character as an item `system`
// with the op_id of the request while the step goes on running (the delivery of
// the outbox itself is in internal/app, in the full suite).
func TestTheRefusalOfAQueuedDoneComesBackAsASystemItemAndTheStepKeepsRunning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n")
	q := w.run(w.create(fmt.Sprintf("name: Fila\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n", n)))
	dev := w.char("Dev Claude 1")
	r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C9=x"}}, "feito")
	if r.OK || r.Error == nil {
		t.Fatalf("a proof of C9 was accepted: %+v", r)
	}
	if err := w.orc.Inbox.QueuedResult(w.ctx, "op-fila-1", dev.ID, "done", r); err != nil {
		t.Fatal(err)
	}
	items, err := w.orc.Inbox.Store().All(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		if it.Kind == inbox.KindSystem && it.Refs.OpID == "op-fila-1" {
			found = true
			if !strings.Contains(it.Text, "op-fila-1") || !strings.Contains(it.Text, "falhou ("+r.Error.Code+")") || !strings.Contains(it.Text, "C9") {
				t.Errorf("the item of the refusal: %q", it.Text)
			}
		}
	}
	if !found {
		t.Errorf("no item system with the op_id: %+v", items)
	}
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
	if got := w.quest(q.ID); got.State != quests.Running {
		t.Errorf("the quest is %s", got.State)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

const criteriaBody = "Login com senha.\n\n## Critérios\n- [ ] C1 aceita senha correta\n- [ ] C2 recusa senha vazia\n- [ ] C3 bloqueia depois de cinco erros\n"
