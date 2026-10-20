package sessionzero_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/sessionzero"
)

// CA-GM-16 (the model; the epic with the section `## Critérios` is read by the
// `mestre table status` of internal/orchestration/tables)
func TestTheModelOfTheGrandMasterHasTheEightLinesOfTheDecisionRuleInOrderTheThreeQuestionsAndTheProofReading(t *testing.T) {
	t.Parallel()
	var all strings.Builder
	for _, r := range (sessionzero.BuiltinModels{}).Current().Rules[sessionzero.RoleGrandMaster] {
		all.WriteString(r.Text + "\n")
	}
	text := all.String()
	at := -1
	for i, line := range []string{
		"  1. already served", "  2. a reply threaded to a message of table #12", "  3. about what an open table does", "  4. same as 3 with the table `in_review`",
		"  5. about something of a table already merged", "  6. same area as an open table but another subject", "  7. unrelated to any table", "  8. more than one table serves",
	} {
		j := strings.Index(text, line)
		if j <= at {
			t.Fatalf("line %d of the decision rule is missing or out of order (at %d, after %d)", i+1, j, at)
		}
		at = j
	}
	for _, want := range []string{"ask up to 3 questions, each with the recommended answer as the first button", "`## Critérios`", "ready with proof", "`mestre choose`", "--from-message <id> --no-wait", "\"Nova mesa\""} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
}

func TestTheLayerOfTheGrandMasterWithTheTriageFitsTheLimitInEveryProvider(t *testing.T) {
	t.Parallel()
	for _, agent := range threeAgents {
		r := newCharRig(t, nil)
		plan := r.prepare(r.req("gm", agent, sessionzero.RoleGrandMaster))
		if n := len(plan.Layer.Text); n > 8000 {
			t.Errorf("%s: the layer of the Grand Master has %d characters", agent, n)
		}
		t.Logf("%s: %d characters, over target %v", agent, len(plan.Layer.Text), plan.Layer.OverTarget)
	}
}
