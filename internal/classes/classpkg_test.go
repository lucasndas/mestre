package classes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

func TestTheHashIsOfTheContentNotOfTheOrderOrTheFolder(t *testing.T) {
	t.Parallel()
	a, err := classes.NewFiles([]classes.File{{Path: "b.md", Data: []byte("1")}, {Path: "a.md", Data: []byte("2")}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := classes.NewFiles([]classes.File{{Path: "a.md", Data: []byte("2")}, {Path: "b.md", Data: []byte("1")}})
	moved, _ := classes.NewFiles([]classes.File{{Path: "a.md", Data: []byte("21")}, {Path: "b.md", Data: []byte("")}})
	if a.Hash() != b.Hash() {
		t.Error("the same files in another order have another hash")
	}
	if a.Hash() == moved.Hash() {
		t.Error("a byte moved from one file to the next has the same hash")
	}
	dir := filepath.Join(t.TempDir(), "qa")
	if err := classes.WriteDir(dir, a); err != nil {
		t.Fatal(err)
	}
	back, err := classes.ReadDir(dir)
	if err != nil || back.Hash() != a.Hash() {
		t.Fatalf("read back: %v, %v", back, err)
	}
	// Replacing a folder leaves no temporary folder behind.
	if err := classes.WriteDir(dir, moved); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Dir(dir))
	if len(ents) != 1 {
		t.Errorf("the scope folder has %d entries after a replace; want 1", len(ents))
	}
}

func TestAPackageThatDoesNotTravelIsRefused(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]string{
		"dotdot": "../x", "absolute": "/etc/x", "backslash": `a\b`, "stream": "a:b", "trailing dot": "a.", "trailing space": "a ", "empty segment": "a//b",
	} {
		if _, err := classes.NewFiles([]classes.File{{Path: p}}); !errors.Is(err, classes.ErrPackage) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := classes.NewFiles([]classes.File{{Path: "big", Data: make([]byte, classes.MaxFileBytes+1)}}); !errors.Is(err, classes.ErrPackage) {
		t.Errorf("a file over the limit: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big"), make([]byte, classes.MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := classes.ReadDir(dir); !errors.Is(err, classes.ErrPackage) {
		t.Errorf("reading a folder with a file over the limit: %v", err)
	}
	if _, err := classes.ReadDir(filepath.Join(dir, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing folder: %v", err)
	}
}

func TestTheAppLinkHasThreeForms(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]classes.AppLink{
		"novo":          {Kind: classes.AppNew},
		"herdar:qa":     {Kind: classes.AppInherit, Class: "qa"},
		"meu-app":       {Kind: classes.AppExisting, Slug: "meu-app"},
		"  novo  ":      {Kind: classes.AppNew},
		"herdar:qa-api": {Kind: classes.AppInherit, Class: "qa-api"},
	} {
		got, err := classes.ParseAppLink(in)
		if err != nil || got != want {
			t.Errorf("%q = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"herdar", "herdar:", "herdar:QA", "Meu App", "a_b"} {
		if _, err := classes.ParseAppLink(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := classes.ParseAppLink(""); !errors.Is(err, classes.ErrAppRequired) {
		t.Errorf("empty: %v", err)
	}
}

func TestExamineListsEveryProblemOfThePackage(t *testing.T) {
	t.Parallel()
	files, _ := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: Outro\ndescription: x\napp: novo\nskills: [nao-existe, rodar]\nprovider: gpt\ncolor: red\n---\ntexto")},
		{Path: "permissions.yaml", Data: []byte("version: 2\n")},
		{Path: "skills/rodar/notes.md", Data: []byte("sem SKILL.md")},
		{Path: "solto.txt", Data: []byte("x")},
	})
	ex := classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassTable, Dir: "qa"})
	// The frontmatter has an unknown field, so it is one problem; the rest of
	// the package is still examined.
	if ex.OK() {
		t.Fatal("the package is valid")
	}
	var all []string
	for _, p := range ex.Problems {
		all = append(all, p.String())
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"color", "version 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("the problems do not mention %q:\n%s", want, text)
		}
	}

	// A readable frontmatter: the name, the provider and the skills.
	files, _ = classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: outro\ndescription: x\napp: novo\nskills: [nao-existe, rodar, mestre-relatorio]\nprovider: gpt\n---\ntexto")},
		{Path: "permissions.yaml", Data: []byte(defaultPerms)},
		{Path: "skills/rodar/notes.md", Data: []byte("sem SKILL.md")},
		{Path: "skills/extra/SKILL.md", Data: []byte("x")},
		{Path: "solto.txt", Data: []byte("x")},
	})
	ex = classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassTable, Dir: "qa", Skill: func(n string) bool { return n == "outra" }})
	all = nil
	for _, p := range ex.Problems {
		all = append(all, p.String())
	}
	text = strings.Join(all, "\n")
	for _, want := range []string{`"outro"`, "diferente do nome da pasta", `"gpt" desconhecido`, `"nao-existe"`, "falta o SKILL.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("the problems do not mention %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "mestre-relatorio") {
		t.Errorf("a skill the Mestre generates was refused:\n%s", text)
	}
	var warns []string
	for _, w := range ex.Warnings {
		warns = append(warns, w.String())
	}
	if w := strings.Join(warns, "\n"); !strings.Contains(w, "solto.txt") || !strings.Contains(w, "skills/extra") {
		t.Errorf("warnings = %s", w)
	}
}

func TestHooksAreReadStrictlyByTheEventsOfTheMestre(t *testing.T) {
	t.Parallel()
	check := func(hooks string) (classes.Examination, string) {
		files := pkg(t, "qa", "novo", defaultPerms, map[string]string{"hooks/hooks.yaml": hooks})
		ex := classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassWorkspace, Dir: "qa"})
		var b []string
		for _, p := range ex.Problems {
			b = append(b, p.String())
		}
		return ex, strings.Join(b, "\n")
	}
	for name, c := range map[string]struct{ yaml, want string }{
		"a native event":   {"PreToolUse:\n  - {name: a, run: x, timeout: 5}\n", "evento \"PreToolUse\" desconhecido"},
		"no timeout":       {"post_tool:\n  - {name: a, run: x}\n", "`timeout`"},
		"blocking in post": {"post_tool:\n  - {name: a, run: x, timeout: 5, blocking: true}\n", "`blocking` só existe em `pre_tool`"},
		"async pre_tool":   {"pre_tool:\n  - {name: a, run: x, timeout: 5, mode: async}\n", "`pre_tool` é `sync`"},
		"bad mode":         {"post_tool:\n  - {name: a, run: x, timeout: 5, mode: later}\n", "`mode` aceita"},
		"bad output":       {"post_tool:\n  - {name: a, run: x, timeout: 5, output: tv}\n", "`output` aceita"},
		"unknown field":    {"post_tool:\n  - {name: a, run: x, timeout: 5, retries: 3}\n", "retries"},
		"when out of tool": {"turn_end:\n  - {name: a, run: x, timeout: 5, when: {kind: [edit]}}\n", "`when` só vale em `pre_tool` e `post_tool`"},
		"bad kind":         {"post_tool:\n  - {name: a, run: x, timeout: 5, when: {kind: [mail]}}\n", `"mail" desconhecido`},
		"repeated name":    {"post_tool:\n  - {name: a, run: x, timeout: 5}\n  - {name: a, run: y, timeout: 5}\n", "já é de outro hook"},
		"missing script":   {"post_tool:\n  - {name: a, run: ./hooks/nao.sh, timeout: 5}\n", "não está no pacote"},
		"two lines":        {"post_tool:\n  - {name: a, run: \"x\\ny\", timeout: 5}\n", "única linha"},
	} {
		if ex, text := check(c.yaml); ex.OK() || !strings.Contains(text, c.want) {
			t.Errorf("%s: problems = %q; want %q", name, text, c.want)
		}
	}
	ex, text := check(`
session_start: [{name: a, run: x, timeout: 5}]
pre_tool:
  - {name: guarda, run: ./hooks/g.sh, run_windows: "g.cmd", timeout: 5, blocking: true, when: {kind: [shell], paths: ["src/**"]}}
turn_end: [{name: testes, run: npm test, mode: async, timeout: 900, output: scroll}]
`)
	_ = text
	// The script of the last case does not exist in the package: only that.
	if len(ex.Problems) != 1 || !strings.Contains(ex.Problems[0].Msg, `"hooks/g.sh" não está no pacote`) {
		t.Fatalf("problems = %v", ex.Problems)
	}
	h := ex.Class.Hooks[providers.EventPreTool][0]
	if h.Command("windows") != "g.cmd" || h.Command("linux") != "./hooks/g.sh" {
		t.Errorf("command = %q / %q", h.Command("windows"), h.Command("linux"))
	}
	if ex.Class.Hooks[providers.EventTurnEnd][0].Mode != classes.ModeAsync || ex.Class.Hooks[providers.EventSessionStart][0].Mode != classes.ModeSync {
		t.Errorf("the mode is not read, or its default (sync) is not applied: %+v", ex.Class.Hooks)
	}
}

func TestAHookAppliesToTheToolCallsItsWhenNames(t *testing.T) {
	t.Parallel()
	h := classes.HookDef{When: &classes.HookWhen{Kind: []providers.ToolKind{providers.KindEdit, providers.KindWrite}, Paths: []string{"src/**"}}}
	wt := newMachine().wt
	m := newMachine()
	edit := func(rel string) providers.Event { return m.file(providers.KindEdit, m.in(rel)) }
	for _, c := range []struct {
		ev   providers.Event
		want bool
	}{
		{edit("src/a/b.go"), true},
		{edit("docs/a.md"), false},
		{m.file(providers.KindRead, m.in("src/a.go")), false},
		{m.shell("npm test"), false},
		{providers.Event{Event: providers.EventTurnEnd}, false},
	} {
		if got := h.Matches(c.ev, wt); got != c.want {
			t.Errorf("%+v = %v; want %v", c.ev.Tool, got, c.want)
		}
	}
	if !(classes.HookDef{}).Matches(providers.Event{Event: providers.EventTurnEnd}, wt) {
		t.Error("a hook with no when must apply to every event")
	}
	if !classes.ProviderLacks(providers.Antigravity, providers.EventPreCompact) || classes.ProviderLacks(providers.Codex, providers.EventPreCompact) {
		t.Error("only the Antigravity lacks pre_compact")
	}
}

func TestASynchronousHookIsCutTwoSecondsBelowTheProvider(t *testing.T) {
	t.Parallel()
	for declared, want := range map[int]time.Duration{5: 5 * time.Second, 28: 28 * time.Second, 29: 28 * time.Second, 600: 28 * time.Second} {
		if got := classes.SyncTimeout(declared, 30); got != want {
			t.Errorf("declared %d s = %v; want %v", declared, got, want)
		}
	}
	if got := classes.SyncTimeout(5, 2); got != time.Second {
		t.Errorf("a provider limit of 2 s leaves %v; want the 1 s floor", got)
	}
}

// CA-TETO-01: the table master saves a class beyond the domains of the
// ceiling and the refusal cites example.com (the check alone is in
// ceiling_test.go).
func TestSavingEnforcesTheCeilingTheScopeAndTheEditOfTheApp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	wide := "version: 1\nnetwork: {internet: true, domains: [\"example.com\"]}\n"
	f.ceiling.set(ceiling(t, "version: 1\npermissions: {network: {domains: [\"github.com\"]}}\n"))
	_, err := f.lib.Save(ctx, classes.SaveRequest{Name: "web", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "web", "", wide, nil), By: tableMaster})
	if !errors.Is(err, classes.ErrCeilingDenied) || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("beyond the ceiling: %v", err)
	}
	// A global class is not cut by the ceiling of one workspace when it is
	// saved: each workspace cuts it when it builds the rules.
	f.save(classes.SaveRequest{Name: "web", Scope: classes.ClassGlobal, App: "novo", Files: pkg(t, "web", "", wide, nil), By: owner})

	// Who may do what.
	for _, c := range []struct {
		scope classes.ClassScope
		by    classes.Author
	}{
		{classes.ClassGlobal, admin}, {classes.ClassGlobal, tableMaster}, {classes.ClassWorkspace, tableMaster},
		{classes.ClassTable, grandMaster}, {classes.ClassGlobal, grandMaster},
		{classes.ClassTable, classes.Author{Level: classes.LevelTableMaster, ID: "mestre-7", Table: "7-outra"}},
	} {
		name := "x-" + string(c.scope) + "-" + strings.ReplaceAll(string(c.by.Level), "_", "-")
		table := ""
		if c.scope == classes.ClassTable {
			table = tableID
		}
		_, err := f.lib.Save(ctx, classes.SaveRequest{Name: name, Scope: c.scope, Table: table, App: "novo", Files: pkg(t, name, "", defaultPerms, nil), By: c.by})
		wantOK := c.by.Level == classes.LevelAdmin
		if (err == nil) != wantOK || (err != nil && !errors.Is(err, classes.ErrNotAuthorized)) {
			t.Errorf("%s by %s: err = %v", c.scope, c.by.Level, err)
		}
	}

	// Editing: the App alone is swapped in the frontmatter, the rest stays;
	// the version goes up; an edit of a class that does not exist is refused.
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: tableMaster})
	f.apps.add("meu-app", classes.AppReady)
	res := f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "meu-app", Edit: true, By: tableMaster})
	if res.App.Link != "meu-app" || res.App.Label != "pronto" || !res.Applied {
		t.Errorf("edit result = %+v", res)
	}
	rec, ok, err := f.records.Approved(ctx, "loja", tableQA)
	if err != nil || !ok || rec.Version != 2 || rec.App != "meu-app" {
		t.Fatalf("record = %+v, %v, %v", rec, ok, err)
	}
	if data, _ := rec.Files.Get("CLASS.md"); !strings.Contains(string(data), "app: meu-app") || strings.Contains(string(data), "app: novo") {
		t.Errorf("CLASS.md = %q", data)
	}
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: "ghost", Scope: classes.ClassTable, Table: tableID, App: "novo", Edit: true, By: tableMaster}); !errors.Is(err, classes.ErrNotVisible) {
		t.Errorf("editing a class that does not exist: %v", err)
	}
	// An App that does not exist is refused; --app against the file too.
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "fantasma", Edit: true, By: tableMaster}); !errors.Is(err, classes.ErrInvalidClass) || !strings.Contains(err.Error(), `"fantasma" não existe`) {
		t.Errorf("an App that does not exist: %v", err)
	}
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: "novo-qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "novo-qa", "meu-app", defaultPerms, nil), By: tableMaster}); err == nil || !strings.Contains(err.Error(), "diverge") {
		t.Errorf("--app against the file: %v", err)
	}
}

// CA-GH-ID-09: a class that inherits an App cannot do more with workflows than
// the class it inherits from (the App of a class made with `never` has no
// Workflows permission).
func TestInheritingAnAppRefusesMoreWorkflowsThanTheOriginHas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.ceiling.set(ceiling(t, "version: 1\npermissions: {git: {workflows: allow}}\n"))
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: admin})
	ci := "version: 1\ngit: {workflows: allow}\n"
	_, err := f.lib.Save(ctx, classes.SaveRequest{Name: "ci", Scope: classes.ClassWorkspace, App: "herdar:qa", Files: pkg(t, "ci", "", ci, nil), By: admin})
	if !errors.Is(err, classes.ErrInvalidClass) || !strings.Contains(err.Error(), "App próprio") {
		t.Fatalf("err = %v; want a refusal that says the class needs an App of its own", err)
	}
	f.save(classes.SaveRequest{Name: "ci", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "ci", "", ci, nil), By: admin})
	// The other way is fine: less than the origin has.
	f.save(classes.SaveRequest{Name: "ci-lite", Scope: classes.ClassWorkspace, App: "herdar:ci", Files: pkg(t, "ci-lite", "", "version: 1\ngit: {workflows: ask}\n", nil), By: admin})
	// A class that does not exist, or that closes a cycle, cannot be inherited.
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: "x", Scope: classes.ClassWorkspace, App: "herdar:nao-existe", Files: pkg(t, "x", "", defaultPerms, nil), By: admin}); !errors.Is(err, classes.ErrInvalidClass) {
		t.Errorf("an origin that does not exist: %v", err)
	}
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: "ci", Scope: classes.ClassWorkspace, App: "herdar:ci-lite", Edit: true, By: admin}); !errors.Is(err, classes.ErrInvalidClass) || !strings.Contains(err.Error(), "ciclo") {
		t.Errorf("a cycle: %v", err)
	}
}

func TestScanListsTheClassesWithTheirStateAgainstTheApprovedVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: admin})
	f.save(classes.SaveRequest{Name: "dev", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "dev", "", defaultPerms, nil), By: tableMaster})
	f.save(classes.SaveRequest{Name: "ops", Scope: classes.ClassGlobal, App: "novo", Files: pkg(t, "ops", "", defaultPerms, nil), By: owner})
	f.put(classes.ClassKey{Scope: classes.ClassWorkspace, Name: "novata"}, pkg(t, "novata", "novo", defaultPerms, nil))

	entries, err := f.lib.Scan(ctx, tableID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]classes.EntryState{}
	for _, e := range entries {
		states[e.Key.Name] = e.State
	}
	want := map[string]classes.EntryState{"qa": classes.EntryApproved, "dev": classes.EntryApproved, "ops": classes.EntryApproved, "novata": classes.EntryNew}
	for n, s := range want {
		if states[n] != s {
			t.Errorf("%s = %q; want %q", n, states[n], s)
		}
	}
	// Another table does not see the table class.
	other, err := f.lib.Scan(ctx, "7-outra")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range other {
		if e.Key.Name == "dev" {
			t.Error("a table class is visible from another table")
		}
	}
	// The folder of an approved class goes away: the approved version holds.
	dir, _ := f.roots.Dir(wsQA)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if c, err := f.lib.Use(ctx, "qa", tableID); err != nil || c.Name != "qa" {
		t.Fatalf("Use after the folder is gone: %v, %v", c, err)
	}
	if _, err := f.lib.Use(ctx, "qa", tableID); err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, a := range f.alerts.all() {
		if a.Kind == classes.ClassMissing {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("the missing folder alerted %d times in two looks; want once", missing)
	}
	// Back to the approved files: the proposal from disk is closed.
	f.put(wsQA, pkg(t, "qa", "novo", "version: 1\nfiles: {write: [\"**\"]}\n", nil))
	if _, err := f.lib.Use(ctx, "qa", tableID); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := f.records.Approved(ctx, "loja", wsQA)
	f.put(wsQA, rec.Files)
	if _, err := f.lib.Use(ctx, "qa", tableID); err != nil {
		t.Fatal(err)
	}
	for _, p := range mustPending(t, f) {
		if p.Key == wsQA {
			t.Errorf("a proposal for qa is still pending after the files went back: %+v", p)
		}
	}
}

func mustPending(t *testing.T, f *fixture) []classes.Proposal {
	t.Helper()
	p, err := f.records.Pending(context.Background(), "loja")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
