package app_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// The CLI of classes and rituals (docs/cli/coordenacao-classes-e-rituais.md):
// the real CLI in the terminal of a character, through the Gate, to the real
// orchestration with the library of classes of the workspace.

const classPerms = "version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"tests/**\"]\ncommands:\n  allow: [\"npm test\"]\n  default: ask\n"

// classTree is the package of a class the CLI reads from a folder.
func classTree(name, perms string) map[string]string {
	return map[string]string{
		"CLASS.md":         "---\nname: " + name + "\ndescription: Testa a mesa\n---\nTeste a mesa e relate as falhas.\n",
		"permissions.yaml": perms,
	}
}

// folder puts a package in a folder of the MESTRE_TMP of the callers.
func (w *commWorld) folder(name string, tree map[string]string) string {
	dir := filepath.Join(w.tmp, name)
	w.trees[filepath.ToSlash(dir)] = tree
	return dir
}

func (w *commWorld) class(name string) (classes.ClassRecord, bool) {
	w.t.Helper()
	rec, err := w.lib.Record(w.ctx, name, w.table.ID)
	return rec, err == nil
}

// CA-CLI-CLR-01
func TestClassCreateWithoutAppIsAppRequiredAndNothingIsMade(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dir := w.folder("qa", classTree("qa", classPerms))
	r := w.run(w.master, "class", "create", "QA", "--from", dir, "--json")
	if v := decodeJSON(t, r); r.code != 2 || v.Error.Code != "app_required" {
		t.Fatalf("class create without --app = %d %s", r.code, r.stdout)
	}
	if _, ok := w.class("qa"); ok {
		t.Error("the class was made")
	}
	if _, err := classes.ReadDir(filepath.Join(w.lib.Roots.Tables, w.table.ID, "classes", "qa")); err == nil {
		t.Error("the folder of the class was written")
	}
}

// CA-CLI-CLR-02
func TestClassCreateAboveTheCeilingIsCeilingDeniedAndCitesTheNetworkItem(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	c, err := classes.ParseCeiling([]byte("version: 1\npermissions: {network: {internet: false}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	w.lib.Ceiling = commCeiling{c}
	dir := w.folder("net", classTree("net", classPerms+"network: {internet: true, domains: [\"example.com\"]}\n"))
	r := w.run(w.master, "class", "create", "net", "--app", "novo", "--from", dir, "--json")
	v := decodeJSON(t, r)
	if r.code != 3 || v.Error.Code != "ceiling_denied" || !strings.Contains(v.Error.Message, "network.internet") {
		t.Fatalf("class create above the ceiling = %d %s", r.code, r.stdout)
	}
	if _, ok := w.class("net"); ok {
		t.Error("the class was made")
	}
}

// Part of CA-CLI-CLR-03: the proposal is made, goes to the Administrators and the Grand Master cannot approve it; its appearing in Telegram and the panel is milestones 5 and 6.
func TestTheGrandMasterClassOfTheWorkspaceIsAProposalForTheAdministratorsNotForHimself(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dir := w.folder("qa", classTree("qa", classPerms))
	r := w.run(w.gm, "class", "create", "qa", "--scope", "workspace", "--app", "novo", "--from", dir, "--json")
	v := decodeJSON(t, r)
	if r.code != 9 || v.Error.Code != "pending" || v.Error.Details["proposal"] == nil {
		t.Fatalf("class create by the Grand Master = %d %s", r.code, r.stdout)
	}
	pend, err := w.lib.Records.Pending(w.ctx, w.slug)
	if err != nil || len(pend) != 1 || pend[0].Key.Name != "qa" || pend[0].Key.Scope != classes.ClassWorkspace {
		t.Fatalf("pending proposals = %+v, %v", pend, err)
	}
	if _, ok := w.class("qa"); ok {
		t.Error("the class is in use before it is approved")
	}
	// the Grand Master never approves what he proposed; an Administrator does
	gm := classes.Author{Level: classes.LevelGrandMaster, ID: w.gm.ID}
	if err := w.lib.Approve(w.ctx, pend[0].ID, pend[0].Hash, gm); !errors.Is(err, classes.ErrNeedsAdministrator) {
		t.Errorf("approval by the Grand Master = %v, want an Administrator needed", err)
	}
	if err := w.lib.Approve(w.ctx, pend[0].ID, pend[0].Hash, classes.Author{Level: classes.LevelAdmin, ID: "u1"}); err != nil {
		t.Errorf("approval by an Administrator = %v", err)
	}
	// and the Mestre da mesa cannot make a class of the workspace
	if r := w.run(w.master, "class", "create", "outra", "--scope", "workspace", "--app", "novo", "--from", dir, "--json"); r.code != 3 || decodeJSON(t, r).Error.Code != "forbidden" {
		t.Errorf("class of the workspace by the Master of the table = %+v", r)
	}
}

// CA-CLI-CLR-04
func TestClassCreateWithANewAppWorksAtOnceAndAsksTheGrandMasterForTheApp(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	w.mach.Report(w.ctx, w.gm.ID, characters.Idle)
	dir := w.folder("qa-api", classTree("qa-api", classPerms))
	r := w.run(w.master, "class", "create", "QA API", "--app", "novo", "--from", dir, "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || !v.OK || v.Data["app_status"] != "solicitado" || v.Data["name"] != "qa-api" || v.Data["scope"] != "table" {
		t.Fatalf("class create --app novo = %d %s", r.code, r.stdout)
	}
	if rec, ok := w.class("qa-api"); !ok || rec.App != "novo" {
		t.Fatalf("class = %+v, %v", rec, ok)
	}
	items, err := w.orc.Inbox.Store().All(w.ctx, w.gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		found = found || it.Kind == inbox.KindSystem && strings.Contains(it.Text, "qa-api") && strings.Contains(it.Text, "mestre app create")
	}
	if !found {
		t.Errorf("the Grand Master was not asked for the App: %+v", items)
	}
}

// CA-CLI-CLR-07
func TestClassCheckShowsASectionForEachProviderAndTheStateOfTheApp(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dir := w.folder("dev", classTree("dev", classPerms))
	if r := w.run(w.master, "class", "create", "Dev", "--app", "novo", "--from", dir); r.code != 0 {
		t.Fatalf("class create = %+v", r)
	}
	r := w.run(w.master, "class", "check", "Dev")
	if r.code != 0 {
		t.Fatalf("class check = %+v", r)
	}
	for _, want := range []string{"Claude Code", "Codex", "Antigravity", "App novo: em criação"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the text has no %q:\n%s", want, r.stdout)
		}
	}
}

// CA-CLI-CLR-10
func TestClassEditFromOutsideTmpIsRefusedAndTheExportLeavesTheApprovedPackage(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dir := w.folder("qa", classTree("qa", classPerms))
	if r := w.run(w.master, "class", "create", "qa", "--app", "novo", "--from", dir); r.code != 0 {
		t.Fatalf("class create = %+v", r)
	}
	before, _ := w.class("qa")
	// a package in the worktree, outside $MESTRE_TMP
	w.trees["/work/rascunho/qa"] = classTree("qa", classPerms+"network: {internet: false}\n")
	r := w.run(w.master, "class", "edit", "qa", "--from", "./rascunho/qa", "--json")
	if v := decodeJSON(t, r); r.code != 2 || v.Error.Code != "from_outside_tmp" {
		t.Fatalf("class edit from the worktree = %d %s", r.code, r.stdout)
	}
	if after, _ := w.class("qa"); after.Hash != before.Hash {
		t.Error("the class changed")
	}
	// the export leaves the approved package in $MESTRE_TMP/classes/qa/
	r = w.run(w.master, "class", "show", "qa", "--export", "--json")
	v := decodeJSON(t, r)
	want := filepath.Join(w.tmp, "classes", "qa")
	if r.code != 0 || v.Data["path"] != want {
		t.Fatalf("class show --export = %d %s", r.code, r.stdout)
	}
	got, err := classes.ReadDir(want)
	if err != nil || got.Hash() != before.Hash {
		t.Fatalf("exported package hash %q (%v), want %q", got.Hash(), err, before.Hash)
	}
	// edit it there and send it back
	edited := map[string]string{}
	for _, f := range got {
		edited[f.Path] = string(f.Data)
	}
	edited["permissions.yaml"] = strings.Replace(classPerms, "default: ask", "default: deny", 1)
	w.trees[filepath.ToSlash(want)] = edited
	if r := w.run(w.master, "class", "edit", "qa", "--from", want, "--json"); r.code != 0 {
		t.Fatalf("class edit from $MESTRE_TMP = %+v", r)
	}
	if after, _ := w.class("qa"); after.Hash == before.Hash || after.Version != before.Version+1 {
		t.Errorf("the edit did not take: %+v", after)
	}
}

// The listing, the assignment and the deletion of classes.
func TestClassListShowAssignAndDeleteFollowTheRulesOfTheDocument(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	dir := w.folder("qa", classTree("qa", classPerms))
	if r := w.run(w.master, "class", "create", "qa", "--app", "novo", "--from", dir); r.code != 0 {
		t.Fatalf("class create = %+v", r)
	}
	if r := w.run(w.master, "class", "create", "qa", "--app", "novo", "--from", dir, "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "name_taken" {
		t.Errorf("a name in use = %+v", r)
	}
	v := decodeJSON(t, w.run(w.master, "class", "list", "--json"))
	list, _ := v.Data["classes"].([]any)
	if len(list) != 1 {
		t.Fatalf("list = %v", v.Data)
	}
	row := list[0].(map[string]any)
	if row["name"] != "qa" || row["scope"] != "table" || row["app"] != "novo" || row["inherited"] != false || row["app_state"] != "solicitado" {
		t.Errorf("row = %v", row)
	}
	if v := decodeJSON(t, w.run(w.master, "class", "list", "--scope", "workspace", "--json")); len(v.Data["classes"].([]any)) != 0 {
		t.Errorf("list --scope workspace = %v", v.Data)
	}
	if r := w.run(w.master, "class", "show", "qa"); r.code != 0 || !strings.Contains(r.stdout, "Teste a mesa") || !strings.Contains(r.stdout, "permissions.yaml") {
		t.Errorf("class show = %+v", r)
	}
	if r := w.run(w.master, "class", "show", "nada", "--json"); r.code != 4 || decodeJSON(t, r).Error.Code != "not_visible" {
		t.Errorf("class show of a class that does not exist = %+v", r)
	}

	// assign: from the next turn on, and only a class the table sees
	if r := w.run(w.master, "class", "assign", "Dev", "qa", "--json"); r.code != 0 {
		t.Fatalf("class assign = %+v", r)
	}
	if got := w.get(dev); classes.Slug(got.Class) != "qa" {
		t.Errorf("the class of Dev is %q", got.Class)
	}
	if r := w.run(w.master, "class", "assign", "Dev", "outra", "--json"); r.code != 4 {
		t.Errorf("assign of a class that does not exist = %+v", r)
	}
	if r := w.run(w.master, "class", "assign", "Ninguem", "qa", "--json"); r.code != 4 {
		t.Errorf("assign to a character that does not exist = %+v", r)
	}

	// delete: not while a character has it, and only at the explicit request of a user
	if r := w.run(w.master, "class", "delete", "qa", "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "in_use" {
		t.Fatalf("delete of a class in use = %+v", r)
	}
	if err := w.orc.Registry.SetClass(w.ctx, dev.ID, "dev"); err != nil {
		t.Fatal(err)
	}
	if r := w.run(w.master, "class", "delete", "qa", "--json"); r.code != 9 || decodeJSON(t, r).Error.Code != "approval_required" {
		t.Fatalf("delete with no explicit request = %+v", r)
	}
	if _, ok := w.class("qa"); !ok || len(w.deleted) != 1 || w.deleted[0].Key.Name != "qa" {
		t.Errorf("the class went (%v) or the request was not made: %+v", ok, w.deleted)
	}
	w.allowed = true
	if r := w.run(w.master, "class", "delete", "qa", "--json"); r.code != 0 {
		t.Fatalf("delete with the request of a user = %+v", r)
	}
	if _, ok := w.class("qa"); ok {
		t.Error("the class is still there")
	}
	// a character is not the Master of the table
	if r := w.run(dev, "class", "list", "--json"); r.code != 3 || decodeJSON(t, r).Error.Code != "class_denied" {
		t.Errorf("class list by a character = %+v", r)
	}
}

// CA-CLI-CLR-05
func TestRitualCreateWithTwoSchedulesIsInvalidScheduleAndNothingIsMade(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	w.recruit("Dev", "Dev Claude")
	r := w.run(w.master, "ritual", "create", "x", "--target", "Dev", "--prompt", "oi", "--daily", "09:00", "--every", "1h", "--json")
	if v := decodeJSON(t, r); r.code != 2 || v.Error.Code != "invalid_schedule" {
		t.Fatalf("ritual create with two schedules = %d %s", r.code, r.stdout)
	}
	if l := decodeJSON(t, w.run(w.master, "ritual", "list", "--json")); len(l.Data["rituals"].([]any)) != 0 {
		t.Errorf("a ritual was made: %v", l.Data)
	}
}

// CA-CLI-CLR-06
func TestRitualWithAPreRunHandsTheTargetTheTextWithTheOutput(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	w.mach.Report(w.ctx, dev.ID, characters.Idle)
	w.pre.out = "quinta-feira"
	if r := w.run(w.master, "ritual", "create", "hoje", "--target", "Dev", "--prompt", "Hoje é {{output}}", "--pre-run", "date", "--daily", "09:00", "--json"); r.code != 0 {
		t.Fatalf("ritual create = %+v", r)
	}
	if r := w.run(w.master, "ritual", "run", "hoje", "--json"); r.code != 0 {
		t.Fatalf("ritual run = %+v", r)
	}
	items, err := w.orc.Inbox.Store().All(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		found = found || it.Kind == inbox.KindRitual && strings.Contains(it.Text, "Hoje é quinta-feira") && !strings.Contains(it.Text, "{{output}}")
	}
	if !found {
		t.Errorf("the target did not get the text with the output: %+v", items)
	}
	// {{output}} with no --pre-run is a usage error
	if r := w.run(w.master, "ritual", "create", "sem", "--target", "Dev", "--prompt", "{{output}}", "--daily", "09:00", "--json"); r.code != 2 {
		t.Errorf("{{output}} without --pre-run = %+v", r)
	}
}

// CA-CLI-CLR-08
func TestRitualAimedAtASheetIsNotVisibleAndNothingIsMade(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	w.recruit("Dev", "Dev Claude")
	r := w.run(w.master, "ritual", "create", "x", "--target", "Dev Claude", "--prompt", "oi", "--daily", "09:00", "--json")
	if v := decodeJSON(t, r); r.code != 4 || v.Error.Code != "not_visible" {
		t.Fatalf("ritual create aimed at a sheet = %d %s", r.code, r.stdout)
	}
	if l := decodeJSON(t, w.run(w.master, "ritual", "list", "--json")); len(l.Data["rituals"].([]any)) != 0 {
		t.Errorf("a ritual was made: %v", l.Data)
	}
}

// CA-CLI-CLR-09
func TestRitualCommandIsTypedWithEnterInTheTerminalWhenItIsFree(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	if r := w.run(w.master, "spawn", "srv", "--base", "bash"); r.code != 0 {
		t.Fatalf("spawn = %+v", r)
	}
	srv, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "srv")
	if err != nil {
		t.Fatal(err)
	}
	w.mach.Report(w.ctx, srv.ID, characters.Idle)
	if r := w.run(w.master, "ritual", "create", "limpa", "--target", "srv", "--command", "make clean", "--every", "1h", "--json"); r.code != 0 {
		t.Fatalf("ritual create = %+v", r)
	}
	if r := w.run(w.master, "ritual", "run", "limpa", "--json"); r.code != 0 {
		t.Fatalf("ritual run = %+v", r)
	}
	if got := string(w.mach.typedRaw(srv.ID)); got != "make clean\r" {
		t.Errorf("the terminal got %q, want make clean and Enter", got)
	}
	// an agent takes no command, a terminal no prompt
	if r := w.run(w.master, "ritual", "create", "c2", "--target", "srv", "--prompt", "oi", "--every", "1h", "--json"); r.code != 2 {
		t.Errorf("a prompt to a terminal = %+v", r)
	}
}

// The rest of the ritual commands: list, show, edit, disable, enable, delete,
// and the scope of who asks.
func TestRitualListShowEditDisableEnableAndDeleteWork(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev", "Dev Claude")
	if r := w.run(w.master, "ritual", "create", "diario", "--target", "Dev", "--prompt", "bom dia", "--daily", "02:00", "--json"); r.code != 0 {
		t.Fatalf("ritual create = %+v", r)
	}
	if r := w.run(w.master, "ritual", "create", "diario", "--target", "Dev", "--prompt", "outra vez", "--daily", "03:00", "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "name_taken" {
		t.Errorf("a name in use = %+v", r)
	}
	v := decodeJSON(t, w.run(w.master, "ritual", "list", "--json"))
	row := v.Data["rituals"].([]any)[0].(map[string]any)
	for _, k := range []string{"name", "target", "quest", "table", "action", "schedule", "pre_run", "timeout", "enabled", "next_run", "last_result"} {
		if _, ok := row[k]; !ok {
			t.Errorf("a ritual of the list has no %q: %v", k, row)
		}
	}
	if row["name"] != "diario" || row["target"] != "Dev" || row["action"] != "prompt" || row["enabled"] != true || row["table"] != w.table.ID {
		t.Errorf("row = %v", row)
	}
	if r := w.run(w.master, "ritual", "edit", "diario", "--every", "30m", "--timeout", "10m", "--json"); r.code != 0 || !strings.Contains(r.stdout, "every 30m") {
		t.Errorf("ritual edit = %+v", r)
	}
	if r := w.run(w.master, "ritual", "edit", "diario", "--every", "1m", "--json"); r.code != 2 {
		t.Errorf("an --every under the minimum = %+v", r)
	}
	if r := w.run(w.master, "ritual", "disable", "diario", "--json"); r.code != 0 || decodeJSON(t, r).Data["enabled"] != false {
		t.Errorf("ritual disable = %+v", r)
	}
	if r := w.run(w.master, "ritual", "run", "diario", "--json"); r.code != 2 {
		t.Errorf("run of a disabled ritual = %+v", r)
	}
	if r := w.run(w.master, "ritual", "enable", "diario", "--json"); r.code != 0 || decodeJSON(t, r).Data["enabled"] != true {
		t.Errorf("ritual enable = %+v", r)
	}
	w.mach.Report(w.ctx, dev.ID, characters.Idle)
	w.run(w.master, "ritual", "run", "diario")
	if r := w.run(w.master, "ritual", "show", "diario"); r.code != 0 || !strings.Contains(r.stdout, "diario") || !strings.Contains(r.stdout, "delivered") {
		t.Errorf("ritual show = %+v", r)
	}
	if r := w.run(w.master, "ritual", "show", "nada", "--json"); r.code != 4 {
		t.Errorf("show of a ritual that does not exist = %+v", r)
	}
	// deleting needs the explicit request of a user
	if r := w.run(w.master, "ritual", "delete", "diario", "--json"); r.code != 9 || decodeJSON(t, r).Error.Code != "approval_required" {
		t.Errorf("ritual delete = %+v", r)
	}
	// the Grand Master has the rituals of the workspace, not the ones of the table
	if l := decodeJSON(t, w.run(w.gm, "ritual", "list", "--json")); len(l.Data["rituals"].([]any)) != 0 {
		t.Errorf("the Grand Master sees %v", l.Data)
	}
	if r := w.run(w.gm, "ritual", "create", "x", "--target", "Mestre", "--prompt", "oi", "--daily", "09:00", "--json"); r.code != 2 || decodeJSON(t, r).Error.Code != "table_required" {
		t.Errorf("a ritual of the Grand Master aimed at a Master with no --table = %+v", r)
	}
	if r := w.run(w.gm, "ritual", "create", "x", "--target", w.master.Name, "--table", w.table.ID, "--prompt", "oi", "--daily", "09:00", "--json"); r.code != 0 {
		t.Errorf("a ritual of the Grand Master aimed at a Master = %+v", r)
	}
	if r := w.run(dev, "ritual", "list", "--json"); r.code != 3 {
		t.Errorf("ritual list by a character = %+v", r)
	}
}
