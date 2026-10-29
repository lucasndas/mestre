package tables_test

import (
	"strings"
	"testing"
)

// CA-MSG-06: the refusal of a command of the tables for an agent comes from the
// catalog in English, with the parameters, and the code is the one of before.
func TestTheRefusalsOfTheTablesAreInEnglishWithParamsAndTheSameCode(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table

	r := w.run(w.gm.ID, "table close", nil, tbl.ID)
	if r.OK || errCode(r) != "not_merged" {
		t.Fatalf("close without the merge = %+v", r.Error)
	}
	if want := "The table " + tbl.ID + " is " + r.Error.Params["state"].(string) + ", with no merged pull request."; r.Error.Message != want {
		t.Errorf("message = %q", r.Error.Message)
	}
	if r.Error.Params["table"] != tbl.ID {
		t.Errorf("params = %v", r.Error.Params)
	}
	if strings.Contains(r.Error.Hint, "merge do PR") || !strings.Contains(r.Error.Hint, "the engine tells you") {
		t.Errorf("hint = %q", r.Error.Hint)
	}

	r = w.run(w.gm.ID, "table open", map[string]any{"epic": "42"})
	if errCode(r) != "table_exists" || r.Error.Params["epic"] != 42 {
		t.Fatalf("a second open = %+v", r.Error)
	}
	if want := "The epic #42 already has the table " + tbl.ID + "."; r.Error.Message != want {
		t.Errorf("message = %q, want %q", r.Error.Message, want)
	}
}

// The text of success of a command of the tables for an agent is in English.
func TestTheTextOfSuccessOfTheTablesIsInEnglish(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	r := w.run(w.gm.ID, "table open", map[string]any{"epic": "42"})
	if !r.OK {
		t.Fatalf("open = %+v", r.Error)
	}
	text, _ := dataOf(t, r)["text"].(string)
	if !strings.HasPrefix(text, "table 42-login opened (branch ") || !strings.Contains(text, "Table Master Mestre recruited") {
		t.Errorf("text = %q", text)
	}
	list := w.run(w.gm.ID, "table list", nil)
	if lt, _ := dataOf(t, list)["text"].(string); !strings.Contains(lt, "- 42-login: working, epic #42") {
		t.Errorf("list text = %q", lt)
	}
}
