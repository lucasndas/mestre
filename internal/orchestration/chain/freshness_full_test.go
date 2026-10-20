//go:build full

package chain_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// moveBaseQuietly moves the base in the mirror without telling the services
// (no OnFetched): the tip changes and nothing has noticed yet.
func (w *world) moveBaseQuietly(file string) {
	w.t.Helper()
	w.git.Handle(w.remote).Data().Commit("main", "feat: "+file, map[string]string{file: "x\n"})
	if err := w.mirror().Fetch(w.ctx, "origin", workspaces.FetchRefspec); err != nil {
		w.t.Fatal(err)
	}
}

// breakingBase is a verification that is green on the head and red on the
// merge: the new commit of the base brings a file the project does not build
// with, so only the base integrated sees it.
func (w *world) breakingBase(breaks *int) func(quests.VerifyRequest) quests.VerifyResult {
	return func(r quests.VerifyRequest) quests.VerifyResult {
		if r.Base == "" {
			return quests.VerifyResult{OK: true}
		}
		*breaks++
		if _, err := w.mirror().ReadFile(w.ctx, r.Base, "breaks.go"); err != nil {
			return quests.VerifyResult{OK: true}
		}
		return quests.VerifyResult{Command: "go build ./...", Output: "breaks.go:1:1: expected 'package'\nFAIL"}
	}
}

// CA-GH-ACOES-11
func TestABaseThatBreaksTheMergeRefusesTheRequestAndTheTableGetsTheStepSyncWhereTheRuleIsAbsent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{freshness: true})
	w.verifier.confirm(true)
	w.devWorks()
	var integrated int
	w.verifier.setRun(w.breakingBase(&integrated))

	// no rule "branch atualizada" and a new commit in the base that breaks the merge
	w.moveBaseQuietly("breaks.go")
	if sq := w.syncMissions(); len(sq) != 0 {
		t.Fatalf("missions of sync = %+v before the gate, want none (nothing noticed the base)", sq)
	}
	id, err := w.openPR(11)
	fe, ok := asFreshnessError(err)
	if !ok || id != 0 {
		t.Fatalf("open = #%d, %v; want the gate refusing the request", id, err)
	}
	for _, want := range []string{"go build ./...", "etapa sync", "expected 'package'", "Nenhum pedido"} {
		if !strings.Contains(fe.Reason, want) {
			t.Errorf("the refusal %q does not say %q", fe.Reason, want)
		}
	}
	runs := w.verifier.runs()
	if len(runs) != 1 || runs[0].Base != w.baseTip() || runs[0].Branch != w.table.Branch || runs[0].Workspace != w.slug {
		t.Fatalf("runs = %+v, want one at the branch of the table with the base %s integrated", runs, w.baseTip())
	}
	if integrated != 1 {
		t.Fatalf("the verification of the merge ran %d times", integrated)
	}
	open, err := w.ch.Store().Open(w.ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("requests = %+v, %v; want none", open, err)
	}
	// the gate made the step of the table
	if sq := w.syncMissions(); len(sq) != 1 || sq[0].State != quests.Running {
		t.Fatalf("missions of sync = %+v after the gate, want the one the gate made, running", sq)
	}

	// the repository with the rule: the gate does not run, GitHub refuses the stale merge
	w.ghRules.set(true)
	id2 := w.mustOpenPR(11)
	if n := len(w.verifier.runs()); n != 1 {
		t.Fatalf("the gate ran %d times with the rule in the repository, want it not to run", n)
	}
	if r := w.request(id2); r.Level.Kind != "grand_master" || len(w.users.askedFor(id2)) != 0 {
		t.Fatalf("request = %+v, want it born at the Grão-Mestre", r.Level)
	}
}
