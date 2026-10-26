package quests_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

// CA-MIS-01: a definition whose `after` points at a later step is refused, and
// the refusal names the id.
func TestADefinitionWithAnAfterToALaterStepIsRefusedWithTheInvalidId(t *testing.T) {
	t.Parallel()
	def, err := quests.Parse([]byte(`
name: Errada
steps:
  - {id: plan, sheet: Arquiteto Claude, after: [dev]}
  - {id: dev,  sheet: Dev Claude, after: [plan]}
`))
	if err != nil {
		t.Fatal(err)
	}
	err = def.Validate()
	var ve *quests.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("validate = %v; want a ValidationError", err)
	}
	if !ve.Has("plan") || !strings.Contains(err.Error(), `"dev"`) {
		t.Errorf("the refusal does not name the step and the invalid id: %v", err)
	}
	if ve.Has("dev") {
		t.Errorf("dev is fine, and was named: %v", err)
	}
}

// The rest of what a definition says by itself: ids, the place of the
// character, the fixed values, the returns.
func TestADefinitionIsCheckedForIdsPlacesAndReturns(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"a repeated id":                 "name: X\nsteps:\n  - {id: a, sheet: S}\n  - {id: a, sheet: S}\n",
		"a step pointing at itself":     "name: X\nsteps:\n  - {id: a, sheet: S, after: [a]}\n",
		"a cycle":                       "name: X\nsteps:\n  - {id: a, sheet: S, after: [b]}\n  - {id: b, sheet: S, after: [a]}\n",
		"no sheet and no character":     "name: X\nsteps:\n  - {id: a}\n",
		"a sheet and a character":       "name: X\nsteps:\n  - {id: a, sheet: S, character: C}\n",
		"a join that is not a join":     "name: X\nsteps:\n  - {id: a, sheet: S}\n  - {id: b, sheet: S, after: [a], join: some}\n",
		"an approval that is not":       "name: X\nsteps:\n  - {id: a, sheet: S, approval: during}\n",
		"a return to a later step":      "name: X\nsteps:\n  - {id: a, sheet: S, can_return_to: b}\n  - {id: b, sheet: S}\n",
		"a return to a step not before": "name: X\nsteps:\n  - {id: a, sheet: S}\n  - {id: b, sheet: S, can_return_to: a}\n",
		"a stuck_after that is no time": "name: X\nsteps:\n  - {id: a, sheet: S, stuck_after: soon}\n",
		"no steps":                      "name: X\nsteps: []\n",
		"no name":                       "steps:\n  - {id: a, sheet: S}\n",
		"a negative number of returns":  "name: X\nmax_returns: -1\nsteps:\n  - {id: a, sheet: S}\n",
		"two steps with the same name":  "name: X\nsteps:\n  - {id: a, name: Same, sheet: S}\n  - {id: b, name: same, sheet: S}\n",
		"an id with a slash":            "name: X\nsteps:\n  - {id: a/b, sheet: S}\n",
	}
	for name, text := range bad {
		def, err := quests.Parse([]byte(text))
		if err == nil {
			err = def.Validate()
		}
		var ve *quests.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: %v; want a ValidationError", name, err)
		}
	}
	if _, err := quests.Parse([]byte("name: X\nstepz: []\n")); err == nil {
		t.Error("a field the format does not know was accepted")
	}
	// The example of the document is valid, in YAML and in JSON, and its defaults are the document's.
	def, err := quests.Parse([]byte(exampleDef))
	if err != nil || def.Validate() != nil {
		t.Fatalf("the example: %v / %v", err, def.Validate())
	}
	if def.Max() != 3 || def.Steps[3].Machine != "windows-pc" || def.Steps[4].Character != "Dev Claude 1" || def.Steps[2].StuckTime().Minutes() != 20 {
		t.Errorf("the example was read as %+v", def)
	}
	js := `{"name":"J","steps":[{"id":"a","sheet":"S"},{"id":"b","sheet":"S","after":["a"],"can_return_to":"a"}]}`
	if def, err := quests.Parse([]byte(js)); err != nil || def.Validate() != nil || def.Max() != quests.DefaultMaxReturns {
		t.Errorf("JSON: %v / %v", err, def.Validate())
	}
	if got := def.Dependents("dev"); !slices.Equal(got, []string{"review", "qa", "fix"}) {
		t.Errorf("dependents of dev = %v", got)
	}
}

// The sheets, the characters and the ceiling are checked when the quest is made.
func TestAQuestNamesOnlySheetsCharactersAndClassesThatExistAndFit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.recruit("Dev Claude", "Dev Claude 1")
	for name, def := range map[string]string{
		"a sheet that does not exist":  "name: A\nsteps:\n  - {id: a, sheet: Fantasma}\n",
		"a character that does not":    "name: B\nsteps:\n  - {id: a, character: Ninguem}\n",
		"a class above the ceiling":    "name: C\nsteps:\n  - {id: a, sheet: Dev Claude, class: root}\n",
		"a sheet whose class is above": "name: D\nsteps:\n  - {id: a, sheet: Root Claude}\n",
	} {
		_, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(def), Designer: w.master.ID})
		var ve *quests.ValidationError
		if !errors.As(err, &ve) || !ve.Has("a") {
			t.Errorf("%s: %v", name, err)
		}
	}
	v := w.create("name: Boa\nsteps:\n  - {id: a, character: Dev Claude 1}\n")
	if v.State != quests.Draft || v.Round != 0 {
		t.Errorf("a quest that was made is %s, round %d; want a draft", v.State, v.Round)
	}
	_, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte("name: boa\nsteps:\n  - {id: a, character: Dev Claude 1}\n")})
	if !errors.Is(err, quests.ErrNameTaken) {
		t.Errorf("the same name again = %v; want name taken", err)
	}
}
