package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The table master in the Citadel world (docs/orquestracao/mestre-da-mesa.md):
// the ceiling of the workspace, the commands that are not its, and the extra
// instructions of the users.

// CA-MM-02
func TestTheTableMasterCannotMakeAClassOfTheTableAboveTheCeilingAndTheErrorListsTheItem(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	w.classCreateAboveTheCeilingIsCeilingDeniedAndCitesTheNetworkItem(t)
}

// Part of CA-MM-03: `mestre reply` and `mestre notify` are the Grand Master's and
// their refusal comes from the gate of the CLI, with the code of the level
// (the three commands together are in
// TestTheTableMasterGetsAPermissionErrorOnReplyNotifyAndAnIssueOfAnotherTable).
func TestTheTableMasterIsDeniedTheCommandsThatTalkToTheUsers(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	for _, args := range [][]string{
		{"reply", "Pronto"},
		{"notify", "falta a chave", "--blocked", "credencial"},
	} {
		r := w.run(w.master, append(args, "--json")...)
		v := decodeJSON(t, r)
		if r.code != 3 || v.OK || v.Error.Code != "level_denied" {
			t.Errorf("%v by the table master = %d %+v, want exit 3 level_denied", args, r.code, v.Error)
		}
	}
}

// CA-MM-03: the table master calls `mestre reply`, `mestre notify` and `mestre
// issue update` on an issue of another table, and each is refused with a
// permission error (exit 3); nothing reaches GitHub and nothing changes.
func TestTheTableMasterGetsAPermissionErrorOnReplyNotifyAndAnIssueOfAnotherTable(t *testing.T) {
	t.Parallel()
	m := &mm{}
	w := newCommWorld(t, m.setup)
	other := m.gh.AddIssue(mmRepo, "de outra mesa")
	if err := m.objs.Register(w.ctx, github.Object{Repo: mmRepo, Kind: github.KindIssue, Number: other, Table: w.slug + "/43-outra", Op: github.NewOp()}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"reply", "Pronto"}, "level_denied"},
		{[]string{"notify", "falta a chave", "--blocked", "credencial"}, "level_denied"},
		{[]string{"issue", "update", fmt.Sprint(other), "--title", "sequestrada"}, "outside_epic"},
	} {
		r := w.run(w.master, append(tc.args, "--json")...)
		v := decodeJSON(t, r)
		if r.code != 3 || v.OK || v.Error.Code != tc.code {
			t.Errorf("%v by the table master = %d ok=%v %+v; want exit 3 %s", tc.args, r.code, v.OK, v.Error, tc.code)
		}
	}
	if title, _, _, _, _ := m.gh.Issue(mmRepo, other); title != "de outra mesa" {
		t.Errorf("the issue of the other table was changed: %q", title)
	}
}

// CA-MM-04
func TestTheExtraInstructionsReachTheLayerOfTwoTableMastersAfterTheRulesOfTheSystem(t *testing.T) {
	t.Parallel()
	w := newSessionWorld(t)
	const extra = "Sempre abra o PR em rascunho."
	w.instructions("mestre-da-mesa.md", extra+"\n")
	_, second := w.openTable(43)
	for _, c := range []characters.Character{w.master, second} {
		req, err := w.build(w.get(c))
		if err != nil {
			t.Fatal(err)
		}
		layer, err := sessionzero.RenderLayer(sessionzero.LayerInput{AgentID: req.ID, Name: req.Name, Role: req.Role, Workspace: req.Workspace, Table: req.Table,
			Machine: req.Machine, Models: (sessionzero.BuiltinModels{}).Current(), CLI: true, Decrees: req.Decrees, Instructions: req.Instructions})
		if err != nil {
			t.Fatal(err)
		}
		rules := layer.Section(sessionzero.SectionRules)
		system, users := strings.Index(rules, "Receive the request as its PO"), strings.Index(rules, "### Instruções dos usuários para os Mestres de mesa")
		if system < 0 || users < system || !strings.Contains(rules[users:], extra) {
			t.Errorf("table master %s: the text of the users is not after the rules of the system:\n%s", req.Table.ID, rules)
		}
	}
}
