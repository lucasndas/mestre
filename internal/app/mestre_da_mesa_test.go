package app_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/sessionzero"
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

// CA-MM-03: `mestre reply` and `mestre notify` are the Grand Master's and
// their refusal comes from the gate of the CLI, with the code of the level;
// an issue of another table is refused by the engine in the same way
// (internal/ghcli, TestAnIssueOutsideTheEpicOfTheTableIsRefused).
func TestTheTableMasterIsDeniedTheCommandsThatTalkToTheUsers(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	for _, args := range [][]string{
		{"reply", "Pronto"},
		{"notify", "falta a chave", "--blocked", "credencial"},
	} {
		r := w.run(w.master, append(args, "--json")...)
		v := decodeJSON(t, r)
		if r.code != 3 || v.OK || v.Error.Code == "" {
			t.Errorf("%v by the table master = %d %+v, want exit 3 (denied)", args, r.code, v.Error)
		}
	}
}

// CA-MM-04
func TestTheExtraInstructionsReachTheLayerOfTwoTableMastersAfterTheRulesOfTheSystem(t *testing.T) {
	t.Parallel()
	w := newSessionWorld(t)
	const extra = "Sempre abra o PR em rascunho."
	w.instructions("mestre-da-mesa.md", extra+"\n")
	_, second := w.openTable(43)
	for _, c := range []string{w.master.ID, second.ID} {
		ch := w.get(w.master)
		if c == second.ID {
			ch = w.get(second)
		}
		req, err := w.build(ch)
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
