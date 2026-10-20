package chain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// CA-GH-ACOES-12
func TestWithoutAConfirmedCommandTheGrandMasterCannotDecideAndNoCommandRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.devWorks()

	id := w.mustOpenPR(12)
	w.humanRequired(id, "sem comando de verificação configurado")
	if runs := w.verifier.runs(); len(runs) != 0 {
		t.Fatalf("a command ran in a workspace with none confirmed: %+v", runs)
	}
	if got := w.targetOf(id); got["base_sha"] != nil {
		t.Fatalf("the target carries a base_sha the gate never ran on: %v", got)
	}
	// the request goes on to the people, never past them: the Grão-Mestre takes it there
	w.decide(w.gm, id, chain.AnswerClimb, "sem comando de verificação")
	if r := w.request(id); r.State != chain.Open || r.Level.Kind != chain.LevelPeople || len(w.users.askedFor(id)) == 0 {
		t.Fatalf("the request is %s at %+v, want open with the people", r.State, r.Level)
	}

	// the commands confirmed: the gate runs as in CA-GH-ACOES-11, and the request is his to decide
	w.verifier.confirm(true)
	id2 := w.mustOpenPR(13)
	runs := w.verifier.runs()
	if len(runs) != 1 || runs[0].Base != w.baseTip() || runs[0].Table != w.table.ID || runs[0].Branch != w.table.Branch {
		t.Fatalf("runs = %+v, want one on the branch of the table with the tip of the base %s", runs, w.baseTip())
	}
	if got := w.decide(w.gm, id2, chain.AnswerApprove, "verificado pelo motor"); got.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved", got)
	}
}

// CA-GH-ACOES-13
func TestABaseThatMovesAfterTheGateInvalidatesTheRequestAndTheGateRunsAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.verifier.confirm(true)
	w.devWorks()

	id := w.mustOpenPR(20)
	first := w.baseTip()
	if got := w.targetOf(id)["base_sha"]; got != first {
		t.Fatalf("base_sha = %v, want the tip %s the gate ran on", got, first)
	}
	if r := w.request(id); r.State != chain.Open {
		t.Fatalf("request is %s before the base moves", r.State)
	}

	w.moveBase("base-1.txt")
	if r := w.request(id); r.State != chain.Invalidated {
		t.Fatalf("request is %s after the base moved, want invalidated", r.State)
	}
	id2 := w.mustOpenPR(20)
	runs := w.verifier.runs()
	if len(runs) != 2 || runs[1].Base != w.baseTip() || runs[1].Base == first {
		t.Fatalf("runs = %+v, want the gate again on the new tip %s", runs, w.baseTip())
	}
	if got := w.targetOf(id2)["base_sha"]; got != w.baseTip() {
		t.Fatalf("base_sha = %v, want the new tip %s", got, w.baseTip())
	}

	// with the rule in the repository the gate does not run: GitHub refuses the stale merge itself
	w.ghRules.set(true)
	id3 := w.mustOpenPR(21)
	if n := len(w.verifier.runs()); n != 2 {
		t.Fatalf("the gate ran %d times; it must not run where the repository asks the branch to be up to date", n)
	}
	if got, ok := w.targetOf(id3)["base_sha"]; ok {
		t.Fatalf("the target carries base_sha %v with the rule in the repository", got)
	}
	w.moveBase("base-2.txt")
	if r := w.request(id3); r.State != chain.Open {
		t.Fatalf("request is %s after the base moved with the rule, want it untouched (open)", r.State)
	}
	if r := w.request(id2); r.State != chain.Invalidated {
		t.Fatalf("the request gated on the old tip is %s, want invalidated", r.State)
	}
}

// cloneCommands is the verification as the executor does it: the commands are
// the ones the main clone of the workspace confirmed, whatever the branch holds.
// Every run is recorded; a command other than "true" fails.
type cloneCommands struct {
	mu   sync.Mutex
	dir  string
	ran  [][]string
	reqs []quests.VerifyRequest
}

func (c *cloneCommands) run(r quests.VerifyRequest) quests.VerifyResult {
	data, err := os.ReadFile(filepath.Join(c.dir, ".mestre", "regras", "verificacao.yaml"))
	if err != nil {
		return quests.VerifyResult{Command: "(unreadable)", Output: err.Error()}
	}
	var f struct {
		Comandos []string `yaml:"comandos"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return quests.VerifyResult{Command: "(invalid)", Output: err.Error()}
	}
	c.mu.Lock()
	c.ran = append(c.ran, f.Comandos)
	c.reqs = append(c.reqs, r)
	c.mu.Unlock()
	for _, cmd := range f.Comandos {
		if cmd != "true" {
			return quests.VerifyResult{Command: cmd, Output: "make: *** [check] Error 1"}
		}
	}
	return quests.VerifyResult{OK: true}
}

// CA-GH-ACOES-15
func TestTheGateAndTheStepSyncRunTheCommandsOfTheMainCloneNotTheOnesOfThePRBranch(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.verifier.confirm(true)
	dev := w.devWorks()

	// the main clone confirmed `make check`; the branch of the PR swaps it for a command that always passes
	clone := filepath.Join(w.orc.Workspaces.Layout().Principal(w.slug), ".mestre", "regras")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "verificacao.yaml"), []byte("version: 1\ncomandos: [\"make check\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.mirror().Data().Commit(w.table.Branch, "chore: verification that always passes", map[string]string{".mestre/regras/verificacao.yaml": "version: 1\ncomandos: [\"true\"]\n"})
	cc := &cloneCommands{dir: w.orc.Workspaces.Layout().Principal(w.slug)}
	w.verifier.setRun(cc.run)

	// the gate: the base moved, and the clone's command is the one that failed
	w.moveBase("base.txt")
	_, err := w.openPR(30)
	fe, ok := asFreshnessError(err)
	if !ok || !strings.Contains(fe.Reason, `"make check"`) {
		t.Fatalf("open = %v, want the gate refusing with the command of the main clone", err)
	}
	if len(cc.ran) != 1 || strings.Join(cc.ran[0], ",") != "make check" {
		t.Fatalf("the gate ran %v, want only the commands of the main clone", cc.ran)
	}
	if cc.reqs[0].Base != w.baseTip() {
		t.Fatalf("the gate asked for the base %s, want the tip %s", cc.reqs[0].Base, w.baseTip())
	}

	// the step sync: the Dev integrates the base and gives the done; the same commands run
	w.integrate()
	if r := w.call(dev.ID, "done", "base integrada"); !r.OK {
		t.Fatalf("done of the sync: %+v", r.Error)
	}
	w.orc.Quests.WaitVerifications()
	if len(cc.ran) != 2 || strings.Join(cc.ran[1], ",") != "make check" {
		t.Fatalf("the sync ran %v, want the commands of the main clone again", cc.ran)
	}
	if cc.reqs[1].Base != "" {
		t.Fatalf("the sync asked the verifier to integrate %q; the Dev already did", cc.reqs[1].Base)
	}
	if sq := w.syncMissions(); len(sq) != 1 || sq[0].State != quests.Running {
		t.Fatalf("the sync is %+v after a red verification, want it still running (the always-passing command of the branch did not end it)", sq)
	}
}

func TestAGateThatCannotBeCheckedOpensNoRequestAndAFreshnessRefusalIsTyped(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.verifier.confirm(true)
	w.devWorks()
	w.ghRules.mu.Lock()
	w.ghRules.err = context.DeadlineExceeded
	w.ghRules.mu.Unlock()
	id, err := w.openPR(40)
	if err == nil || id != 0 {
		t.Fatalf("open = #%d, %v; want an error and no request when the rules of the branch could not be read", id, err)
	}
	if _, ok := asFreshnessError(err); ok {
		t.Fatalf("an unreadable gate is a refusal of the verification: %v", err)
	}
	if open, _ := w.ch.Store().OpenOfTable(w.ctx, w.slug, w.table.ID); len(open) != 0 {
		t.Fatalf("requests = %+v, want none", open)
	}

	// the branch of the mirror is not the head of the PR: what is proved is not what is merged
	w.ghRules.mu.Lock()
	w.ghRules.err = nil
	w.ghRules.mu.Unlock()
	_, err = w.ch.Request(w.ctx, chain.OpenRequest{Type: classes.RequestPR, Workspace: w.slug, Table: w.table.ID,
		PR: &chain.PRRef{Repo: "lucas/Meu_App", Number: 41, SHA: strings.Repeat("0", 40)}, Target: []byte(`{"pr":41}`)})
	if err == nil || len(w.verifier.runs()) != 0 {
		t.Fatalf("open = %v with %d runs, want an error before any run", err, len(w.verifier.runs()))
	}
}

func TestARequestBornBeforeTheGateAppliedIsNotDecidedAloneAfterwards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.devWorks()
	id := w.mustOpenPR(50) // no command confirmed: no gate
	w.verifier.confirm(true)
	w.humanRequired(id, "gate de frescor não rodou")
	// the rule in the repository when it was born, gone now
	w.ghRules.set(true)
	id2 := w.mustOpenPR(51)
	w.ghRules.set(false)
	w.humanRequired(id2, "gate de frescor não rodou")
}

func TestABaseThatMovesWhileTheGateRunsLeavesNoRequest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.verifier.confirm(true)
	w.devWorks()
	moved := false
	w.verifier.setRun(func(r quests.VerifyRequest) quests.VerifyResult {
		if r.Head != w.refTip("refs/heads/"+w.table.Branch) {
			t.Errorf("the gate asked for the head %q", r.Head)
		}
		if !moved {
			moved = true
			w.moveBase("while-gate.txt")
		}
		return quests.VerifyResult{OK: true}
	})
	id, err := w.openPR(60)
	if _, ok := asFreshnessError(err); !ok || id != 0 {
		t.Fatalf("open = #%d, %v; want the gate refusing: the base moved under it", id, err)
	}
	if open, _ := w.ch.Store().OpenOfTable(w.ctx, w.slug, w.table.ID); len(open) != 0 {
		t.Fatalf("requests = %+v, want none open", open)
	}
	if _, err := w.openPR(60); err != nil {
		t.Fatalf("the second try = %v, want it to pass on the new tip", err)
	}
}

func TestTheTargetOfAPRCarriesTheTipOfTheBaseOnlyAsAnObject(t *testing.T) {
	t.Parallel()
	got, err := chain.WithBaseSHA([]byte(`{"pr":1,"head":"h"}`), "b1")
	if err != nil || chain.BaseSHAOf(got) != "b1" {
		t.Fatalf("WithBaseSHA = %s, %v", got, err)
	}
	if chain.BaseSHAOf([]byte(`{"pr":1}`)) != "" || chain.BaseSHAOf([]byte(`nope`)) != "" {
		t.Fatal("a target without base_sha has one")
	}
	for _, bad := range []string{`[1]`, `null`, `"x"`, ``} {
		if _, err := chain.WithBaseSHA([]byte(bad), "b1"); err == nil {
			t.Errorf("a target %q took a base_sha", bad)
		}
	}
}
