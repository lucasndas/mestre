package quests_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// stoppedNames lists the names of the characters the machines were told to end.
func (w *world) stoppedNames() []string {
	w.t.Helper()
	var out []string
	for _, id := range w.machines.StoppedIDs() {
		out = append(out, w.get(id).Name)
	}
	return out
}

// CA-MIS-02: the quest of the example runs with fake agents. review and qa
// start together after dev, fix only after both of them, and the scroll of the
// quest has a section per step with the summary and the files the fake git
// reports.
func TestTheQuestOfTheExampleRunsReviewAndQaTogetherAndFixAfterBoth(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev Claude 1") // the character `fix` names; `dev` takes it, free
	v := w.create(exampleDef)
	q := w.run(v)
	if q.State != quests.Running || q.Round != 1 {
		t.Fatalf("after run: %s, round %d", q.State, q.Round)
	}

	// plan starts alone, with a character the engine made for it.
	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepRunning, "dev": quests.StepPending, "review": quests.StepPending, "qa": quests.StepPending, "fix": quests.StepPending})
	arch := w.char("Arquiteto Claude 1")
	if arch.CreatedBy.Kind != characters.CreatedByEngine || arch.Quest != q.ID || arch.Step != q.ID+"/plan" || arch.Lifecycle != characters.Ephemeral {
		t.Errorf("the character of plan: %+v", arch)
	}
	if item := w.itemOf("Arquiteto Claude 1"); !strings.Contains(item, "Planeje o login") || !strings.Contains(item, "mestre done") || !strings.Contains(item, ".mestre/mesas/"+w.table.ID+"/pergaminhos/") {
		t.Errorf("the item of plan:\n%s", item)
	}
	res := w.finish("Arquiteto Claude 1", "Plano pronto", map[string]string{"docs/plano.md": "# plano\n"}, "")
	if res.Step != "plan" || res.Quest != "Login com testes" || res.ReturnedTo != nil || res.QuestState != "running" {
		t.Errorf("done of plan = %+v", res)
	}

	// dev takes the persistent character that is free, with the result of plan.
	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepCompleted, "dev": quests.StepRunning})
	if w.step(q, "dev").Character != dev.ID {
		t.Errorf("dev did not take the free persistent character")
	}
	if item := w.itemOf("Dev Claude 1"); !strings.Contains(item, "Plano pronto") || !strings.Contains(item, "A docs/plano.md") {
		t.Errorf("dev did not get the result of plan:\n%s", item)
	}
	w.finish("Dev Claude 1", "Login feito", map[string]string{"src/login.go": "package login\n", "src/login_test.go": "package login\n"}, "")

	// review and qa begin together; fix waits for both.
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepCompleted, "review": quests.StepRunning, "qa": quests.StepRunning, "fix": quests.StepPending})
	qa := w.char("QA Antigravity 1")
	if qa.Machine != "torre" {
		t.Errorf("qa runs on %s; want the machine windows-pc (torre)", qa.Machine)
	}
	if w.peers.Sends() == 0 {
		t.Error("the branch of the table did not go to the Tower before the step that runs there")
	}
	w.finish("Revisor Codex 1", "Aprovado", nil, "")
	w.wantStates(q, map[string]quests.StepState{"review": quests.StepCompleted, "qa": quests.StepRunning, "fix": quests.StepPending})
	w.towers.warnings = []string{"alterações não commitadas não foram sincronizadas: notas.txt"}
	res = w.finish("QA Antigravity 1", "Testes ok", map[string]string{"tests/e2e.txt": "ok\n"}, "")
	if len(w.towers.calls) != 1 || !slices.ContainsFunc(res.Warnings, func(s string) bool { return strings.Contains(s, "notas.txt") }) {
		t.Errorf("the done of a Tower did not send its commits and carry the warning: %v / %+v", w.towers.calls, res)
	}

	// fix: the Dev Claude 1 again, with both results.
	w.wantStates(q, map[string]quests.StepState{"qa": quests.StepCompleted, "fix": quests.StepRunning})
	item := w.itemOf("Dev Claude 1")
	if !strings.Contains(item, "Aprovado") || !strings.Contains(item, "Testes ok") {
		t.Errorf("fix did not get the results of review and qa:\n%s", item)
	}
	res = w.finish("Dev Claude 1", "Correções", map[string]string{"src/login.go": "package login // fixed\n"}, "")
	if res.QuestState != "completed" {
		t.Errorf("the last done says the quest is %q", res.QuestState)
	}
	q = w.quest(q.ID)
	if q.State != quests.Completed {
		t.Fatalf("the quest is %s; want completed", q.State)
	}

	// The scroll: a section per step, with the summary and the files.
	text := w.scroll(q)
	for _, want := range []string{
		"## Rodada 1 · 1. plan — Arquiteto Claude 1 —", "**Resumo:** Plano pronto", "**Arquivos:** A docs/plano.md",
		"## Rodada 1 · 2. dev — Dev Claude 1 —", "**Resumo:** Login feito", "A src/login.go, A src/login_test.go",
		"## Rodada 1 · 3. review — Revisor Codex 1 —", "**Resumo:** Aprovado", "**Arquivos:** (nenhum)",
		"## Rodada 1 · 4. qa — QA Antigravity 1 —", "A tests/e2e.txt", "**Não commitados:** notas.txt",
		"## Rodada 1 · 5. fix — Dev Claude 1 —", "**Resumo:** Correções", "M src/login.go",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "· 3. review") > strings.Index(text, "· 4. qa") || strings.Index(text, "· 4. qa") > strings.Index(text, "· 5. fix") {
		t.Errorf("the sections are out of order:\n%s", text)
	}

	// The quest ended: the ephemeral characters it made are ended, the
	// persistent one stays.
	stopped := w.stoppedNames()
	for _, name := range []string{"Arquiteto Claude 1", "Revisor Codex 1", "QA Antigravity 1"} {
		if !slices.Contains(stopped, name) {
			t.Errorf("%s was not ended with the quest (ended: %v)", name, stopped)
		}
	}
	if slices.Contains(stopped, "Dev Claude 1") || w.get(dev.ID).State == characters.Stopped {
		t.Error("the persistent character was ended with the quest")
	}
}
