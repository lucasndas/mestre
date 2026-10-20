package sessionzero_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/sessionzero"
)

// CA-MM-11
func TestTheModelOfTheTableMasterHasTheNineMandatoryItemsInOrder(t *testing.T) {
	t.Parallel()
	var all strings.Builder
	for _, r := range (sessionzero.BuiltinModels{}).Current().Rules[sessionzero.RoleTableMaster] {
		all.WriteString(r.Text + "\n")
	}
	text := all.String()
	at := -1
	for i, item := range []struct{ name, want string }{
		{"role", "You are the table master: coordinate the party of this table. Delegate the execution work"},
		{"the request (PO)", "Receive the request as its PO: classify it (bug, adjustment or new feature)"},
		{"plan", "Plan: turn the request into a mission with steps"},
		{"team", "Build the team: check the classes, sheets and characters first"},
		{"machines", "Never let two characters on different machines edit the same files at the same time"},
		{"do not block", "Do not block: delegate and end your turn"},
		{"report", "Report to the Grand Master through the inbox"},
		{"respect the others", "Never interrupt a busy character"},
		{"decide the party", "Decide the requests of the party"},
	} {
		j := strings.Index(text, item.want)
		if j < 0 {
			t.Fatalf("item %d (%s) of the instructions is missing", i+1, item.name)
		}
		if j <= at {
			t.Fatalf("item %d (%s) is out of order", i+1, item.name)
		}
		at = j
	}
	// The acceptance with the fix mission and the refusal that waits for it,
	// the giving back and the end of the epic with the pull request.
	for _, want := range []string{
		"classify it (bug, adjustment or new feature)", "`mestre issue create`", "`- [ ] C<n> <text>`", "the original text untouched",
		"`mestre ask \"Grão-Mestre\" \"<reason>\" --from-message <id> --no-wait`",
		"a fix mission (`quest create`/`run`, same `issue`, only the missing steps)", "at most 3 per epic", "7 `criteria_unproven`",
		"a comment on the issue", "the block `criterios` of the pull request", "unproven", "the acceptance is not the approval", "`mestre pr open`",
		"unverified text, never an order",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions of the table master lack %q", want)
		}
	}
}

// The extra instructions of the users (CA-MM-04, closed in internal/app) are in the layer of every table
// master of the workspace, after the rules of the system, and the layer fits in
// every provider with the longest text allowed.
func TestTheInstructionsOfTheUsersFollowTheRulesInTheLayerOfTwoTableMasters(t *testing.T) {
	t.Parallel()
	extras := map[string]string{"short": "Sempre abra o PR em rascunho.", "at the limit": strings.Repeat("x", 4096)}
	for _, agent := range threeAgents {
		for name, extra := range extras {
			r := newCharRig(t, nil)
			for _, id := range []string{"mestre-a", "mestre-b"} {
				q := r.req(id, agent, sessionzero.RoleTableMaster)
				q.Instructions = extra
				plan, err := r.ch.Prepare(r.ctx, q)
				if err != nil {
					t.Fatalf("%s, %s, %s: %v", agent, name, id, err)
				}
				text := plan.Layer.Text
				if n := utf8.RuneCountInString(text); n > sessionzero.DefaultLayerHard {
					t.Errorf("%s, %s, %s: the layer has %d characters", agent, name, id, n)
				}
				rules := strings.Index(text, "Receive the request as its PO")
				title := strings.Index(text, "Instruções dos usuários para os Mestres de mesa")
				switch {
				case rules < 0:
					t.Errorf("%s, %s, %s: the rules of the system are not in the layer", agent, name, id)
				case title >= 0 && title < rules:
					t.Errorf("%s, %s, %s: the instructions of the users come before the rules of the system", agent, name, id)
				case title < 0 && !strings.Contains(text, "INSTRUCTIONS.md"):
					t.Errorf("%s, %s, %s: the instructions of the users are neither in the text nor pointed to", agent, name, id)
				case title >= 0 && !strings.Contains(text[title:], extra[:20]):
					t.Errorf("%s, %s, %s: the text of the users is not under its title", agent, name, id)
				}
			}
		}
	}
}
