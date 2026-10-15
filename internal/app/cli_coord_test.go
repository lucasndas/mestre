package app_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// CA-CLI-COORD-01: a sheet that needs a GPU and no machine has one: exit 4,
// no_capable_machine, and the message says what each machine lacks.
func TestRecruitWithNoCapableMachineSaysWhatEachOneLacks(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	r := w.run(w.master, "recruit", "QA-1", "--sheet", "QA Codex", "--json")
	if r.code != 4 || r.stderr != "" {
		t.Fatalf("recruit = %+v", r)
	}
	v := decodeJSON(t, r)
	if v.OK || v.Error.Code != "no_capable_machine" || !strings.Contains(v.Error.Message, "Cidadela: sem GPU") || !strings.Contains(v.Error.Message, "windows-pc: sem GPU") {
		t.Errorf("error = %+v", v.Error)
	}
	if _, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "QA-1"); err == nil {
		t.Error("a character was made with no machine to run it")
	}
	if r := w.run(w.master, "recruit", "QA-1", "--sheet", "QA Codex"); !strings.Contains(r.stderr, "error: ") || !strings.Contains(r.stderr, "\nhint: ") {
		t.Errorf("the text of the error: %q", r.stderr)
	}
}

// CA-CLI-COORD-02: an ordinary character is refused by the level.
func TestRecruitByAnOrdinaryCharacterIsLevelDenied(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	r := w.run(dev, "recruit", "X", "--sheet", "Dev Claude", "--json")
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "level_denied" {
		t.Fatalf("recruit by a character = %d %+v", r.code, v)
	}
	if _, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "X"); err == nil {
		t.Error("the character was made")
	}
}

// CA-CLI-COORD-03: a pinned character is not dismissed.
func TestDismissOfAPinnedCharacterIsPinnedAndItStaysAlive(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	if err := w.orc.Registry.SetPinned(w.ctx, dev.ID, true); err != nil {
		t.Fatal(err)
	}
	r := w.run(w.master, "dismiss", "Dev", "--json")
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "pinned" {
		t.Fatalf("dismiss of a pinned character = %d %+v", r.code, v)
	}
	if got := w.get(dev); !got.State.Alive() {
		t.Errorf("the pinned character is %s", got.State)
	}
}

// CA-CLI-COORD-08: a busy character is dismissed at the next safe point.
func TestDismissOfABusyCharacterIsScheduledAndItStaysAliveUntilTheSafePoint(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	w.mach.Report(w.ctx, dev.ID, characters.Busy)
	r := w.run(w.master, "dismiss", "Dev", "--json")
	v := decodeJSON(t, r)
	if r.code != 9 || v.Error.Code != "scheduled" || v.Data["status"] != "scheduled" || v.Data["name"] != "Dev" {
		t.Fatalf("dismiss of a busy character = %d %s", r.code, r.stdout)
	}
	if got := w.get(dev); !got.State.Alive() || got.StopRequestedAt.IsZero() {
		t.Errorf("the busy character is %s, stop requested at %v", got.State, got.StopRequestedAt)
	}
	if text := w.run(w.master, "dismiss", "Dev"); text.code != 9 || !strings.Contains(text.stderr, "next safe point") {
		t.Errorf("text = %+v", text)
	}
	// It reaches the safe point: its done, its turn ends, and the engine ends it.
	w.turnEnds(dev, "")
	if err := w.orc.Characters.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.get(dev); got.State != characters.Stopped {
		t.Errorf("after the safe point the character is %s", got.State)
	}
}

// CA-CLI-COORD-05: there is no free command: `spawn` has no such option.
func TestSpawnTakesNoFreeCommand(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	r := w.run(w.master, "spawn", "srv", "--base", "bash", "--command", "rm -rf /", "--json")
	if v := decodeJSON(t, r); r.code != 2 || v.Error.Code != "usage" || !strings.Contains(v.Error.Message, "--command") {
		t.Fatalf("spawn with --command = %d %+v", r.code, v)
	}
	if _, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "srv"); err == nil {
		t.Error("the terminal was made")
	}
}

// recruit, spawn and dismiss as the table master uses them: the text, the
// --json of the document and the errors that do not have a criterion.
func TestRecruitSpawnAndDismissAnswerAsTheDocumentSays(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	r := w.run(w.master, "recruit", "Dev-2", "--sheet", "Dev Claude", "--class", "Dev")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "recruited Dev-2 (class Dev, machine Cidadela, ") {
		t.Fatalf("recruit text = %+v", r)
	}
	v := decodeJSON(t, w.run(w.master, "recruit", "Dev-3", "--sheet", "Dev Claude", "--json"))
	for _, k := range []string{"name", "id", "sheet", "class", "machine", "status", "connected_to"} {
		if _, ok := v.Data[k]; !ok {
			t.Errorf("recruit --json has no %q: %v", k, v.Data)
		}
	}
	if to, _ := v.Data["connected_to"].([]any); len(to) != 1 || to[0] != "Mestre" {
		t.Errorf("connected_to = %v", v.Data["connected_to"])
	}
	// the new character is connected to who recruited it
	if r := w.run(w.master, "ask", "Dev-3", "oi", "--no-wait"); r.code != 0 {
		t.Errorf("ask the recruited character = %+v", r)
	}
	// errors: name in use, a sheet that does not exist, a machine that does not exist, no --sheet
	if r := w.run(w.master, "recruit", "Dev-2", "--sheet", "Dev Claude", "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "name_taken" {
		t.Errorf("name in use = %+v", r)
	}
	if r := w.run(w.master, "recruit", "Z", "--sheet", "Não Existe", "--json"); r.code != 4 || !strings.Contains(decodeJSON(t, r).Error.Message, "Dev Claude") {
		t.Errorf("a sheet that does not exist = %+v (it lists the valid ones)", r)
	}
	if r := w.run(w.master, "recruit", "Z", "--sheet", "Dev Claude", "--machine", "nenhuma", "--json"); r.code != 4 {
		t.Errorf("a machine that does not exist = %+v", r)
	}
	if r := w.run(w.master, "recruit", "Z"); r.code != 2 {
		t.Errorf("recruit with no sheet = %+v", r)
	}

	// spawn: a simple terminal, connected to who made it; --json of the document
	r = w.run(w.master, "spawn", "srv", "--base", "bash", "--json")
	sv := decodeJSON(t, r)
	if r.code != 0 || sv.Data["name"] != "srv" || sv.Data["base"] != "bash" || sv.Data["machine"] != "Cidadela" || sv.Data["id"] == "" || sv.Data["status"] == nil {
		t.Fatalf("spawn = %+v", r)
	}
	srv, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "srv")
	if err != nil || srv.Kind != characters.KindTerminal {
		t.Fatalf("the terminal: %+v, %v", srv, err)
	}
	if r := w.run(w.master, "spawn", "x", "--base", "nenhuma", "--json"); r.code != 4 {
		t.Errorf("a base that does not exist = %+v", r)
	}

	// dismiss: an idle character goes at once; another table's is not visible
	r = w.run(w.master, "dismiss", "Dev-2", "--json")
	if dv := decodeJSON(t, r); r.code != 0 || dv.Data["status"] != "dismissed" {
		t.Fatalf("dismiss = %+v", r)
	}
	if got, _ := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "Dev-2"); got.ID != "" && got.State.Alive() {
		t.Errorf("Dev-2 is %s", got.State)
	}
	if r := w.run(w.master, "dismiss", "Fantasma", "--json"); r.code != 4 || decodeJSON(t, r).Error.Code != "not_visible" {
		t.Errorf("dismiss of a name nobody has = %+v", r)
	}
}

// The Grand Master recruits the master of a table and the Operator, and no
// one else; it names the table with --table.
func TestTheGrandMasterRecruitsOnlyMastersAndTheOperator(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	if r := w.run(w.gm, "recruit", "Dev", "--sheet", "Dev Claude", "--table", w.table.ID, "--json"); r.code != 3 || decodeJSON(t, r).Error.Code != "level_denied" {
		t.Errorf("the Grand Master recruits a developer = %+v", r)
	}
	if r := w.run(w.gm, "recruit", "Dev", "--sheet", "Dev Claude", "--json"); r.code != 2 || decodeJSON(t, r).Error.Code != "table_required" {
		t.Errorf("no --table = %+v", r)
	}
	if r := w.run(w.gm, "spawn", "x", "--base", "bash"); r.code != 3 {
		t.Errorf("the Grand Master spawns = %+v", r)
	}
	// the Operator: an approval is asked for, and the character is born after it
	r := w.run(w.gm, "recruit", "Op", "--sheet", "Operador", "--task", "abrir o navegador", "--for", "45m", "--table", w.table.ID, "--json")
	v := decodeJSON(t, r)
	if r.code != 9 || v.Error.Code != "approval_pending" || v.Data["request"] != "7" {
		t.Fatalf("recruit of the Operator = %d %s", r.code, r.stdout)
	}
	if len(w.oper.got) != 1 || w.oper.got[0].Task != "abrir o navegador" || w.oper.got[0].For.Minutes() != 45 || w.oper.got[0].Table != w.table.ID {
		t.Errorf("the request: %+v", w.oper.got)
	}
	if r := w.run(w.gm, "recruit", "Op", "--sheet", "Operador", "--table", w.table.ID); r.code != 2 {
		t.Errorf("the Operator with no --task = %+v", r)
	}
	if r := w.run(w.master, "recruit", "Op", "--sheet", "Operador", "--task", "x"); r.code != 9 {
		t.Errorf("the table master asks for the Operator = %+v", r)
	}
}

// `sheet` and `base` only read, and show no secrets.
func TestSheetAndBaseListAndShowWithoutSecrets(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	r := w.run(w.master, "sheet", "list", "--json")
	v := decodeJSON(t, r)
	rows, _ := v.Data["sheets"].([]any)
	if r.code != 0 || len(rows) != len(commSheets) {
		t.Fatalf("sheet list = %+v", r)
	}
	for _, row := range rows {
		m := row.(map[string]any)
		if m["name"] == "Dev Claude" {
			if m["base"] != "claude-code" || m["agent"] != "claude-code" || m["class"] != "Dev" || m["lifecycle"] != "persistent" {
				t.Errorf("row = %v", m)
			}
			if fits, _ := m["machines"].([]any); len(fits) != 1 || fits[0] != "Cidadela" {
				t.Errorf("machines = %v", m["machines"])
			}
		}
		if m["name"] == "QA Codex" {
			if fits, _ := m["machines"].([]any); len(fits) != 0 {
				t.Errorf("QA Codex fits %v", fits)
			}
		}
	}
	if r := w.run(w.master, "sheet", "show", "Dev Claude"); r.code != 0 || !strings.Contains(r.stdout, "base: claude-code") {
		t.Errorf("sheet show = %+v", r)
	}
	if r := w.run(w.master, "sheet", "show", "Nenhuma", "--json"); r.code != 4 {
		t.Errorf("sheet show of a sheet that does not exist = %+v", r)
	}
	r = w.run(w.master, "base", "show", "claude-code", "--json")
	if r.code != 0 || strings.Contains(r.stdout, "s3cr3t-value") || !strings.Contains(r.stdout, "TOKEN") {
		t.Errorf("base show shows the value of the environment: %+v", r)
	}
	if r := w.run(w.master, "base", "list"); r.code != 0 || !strings.Contains(r.stdout, "bash") {
		t.Errorf("base list = %+v", r)
	}
	if r := w.run(w.gm, "base", "list"); r.code != 0 {
		t.Errorf("the Grand Master reads the bases: %+v", r)
	}
	dev := w.recruit("Dev", "Dev Claude")
	if r := w.run(dev, "sheet", "list", "--json"); r.code != 3 {
		t.Errorf("an ordinary character reads the sheets = %+v", r)
	}
}
