package presets_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/presets"
)

func hookBase(name string) presets.Base {
	return presets.Base{Name: name, Command: "claude", Platforms: []presets.Platform{{OS: presets.Linux}}, Ready: presets.Ready{Mode: presets.ReadyHook}}
}

func TestABaseIsRefusedForWhatItCarries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		edit   func(b *presets.Base)
		reason string
	}{
		{"no platform", func(b *presets.Base) { b.Platforms = nil }, "nenhuma plataforma"},
		{"a system that does not exist", func(b *presets.Base) { b.Platforms[0].OS = "macos" }, "`linux` ou `windows`"},
		{"a name with capitals", func(b *presets.Base) { b.Name = "Claude" }, "só letras minúsculas"},
		{"no command", func(b *presets.Base) { b.Command = " " }, "`command` é obrigatório"},
		{"an unknown shell", func(b *presets.Base) { b.Shell = "zsh" }, "`shell` aceita"},
		{"a start folder outside the table", func(b *presets.Base) { b.Cwd = "{home}/x" }, "fora de `{mesa}`"},
		{"a start folder that climbs out of it", func(b *presets.Base) { b.Cwd = "{mesa}/../x" }, "fora de `{mesa}`"},
		{"an unknown variable", func(b *presets.Base) { b.Cwd = "{mesa}/{dia}" }, "variável `{dia}`"},
		{"a secret in the environment", func(b *presets.Base) { b.Env = map[string]string{"X": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"} }, "parece um segredo"},
		{"a secret in an argument", func(b *presets.Base) { b.Args = []string{"--name", "sk-abcdefghijklmnopqrstuvwxyz0123"} }, "parece um segredo"},
		{"a variable that moves the hooks", func(b *presets.Base) { b.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/x"} }, "CLAUDE_CONFIG_DIR"},
		{"a variable that runs code first", func(b *presets.Base) { b.Env = map[string]string{"ld_preload": "/tmp/x.so"} }, "ld_preload"},
		{"a flag that skips the permissions", func(b *presets.Base) { b.Args = []string{"--dangerously-skip-permissions"} }, "forbidden"},
		{"a prompt", func(b *presets.Base) { b.Args = []string{"fix the bug"} }, "not an option"},
		{"a pattern that is not a regex", func(b *presets.Base) { b.Ready = presets.Ready{Mode: presets.ReadyPattern, Pattern: "("} }, "expressão regular"},
		{"a delay out of range", func(b *presets.Base) { b.Ready = presets.Ready{Mode: presets.ReadyDelay, Delay: 0} }, "`ready.delay`"},
		{"no ready", func(b *presets.Base) { b.Ready = presets.Ready{} }, "`ready` é obrigatório"},
		{"a requirement that does not exist", func(b *presets.Base) { b.Requires = map[string]any{"cor": "azul"} }, "`cor` desconhecido"},
		{"a machine twice", func(b *presets.Base) { b.Machines = []string{"m1", "m1"} }, "repete `m1`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := hookBase("mine")
			tt.edit(&b)
			err := presets.ValidateBase(b)
			if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("err = %v; want a refusal with %q", err, tt.reason)
			}
		})
	}
}

func TestAShellBaseMayStartAnywhereAndAnyVariableButTheMestres(t *testing.T) {
	t.Parallel()
	b := presets.Base{Name: "bash", Command: "bash", Platforms: []presets.Platform{{OS: presets.Linux}},
		Ready: presets.Ready{Mode: presets.ReadyPattern, Pattern: `\$ $`}, Cwd: "{home}/dev", Env: map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096"}}
	if err := presets.ValidateBase(b); err != nil {
		t.Errorf("a shell: %v", err)
	}
	b.Env = map[string]string{"Mestre_Token": "x"}
	if err := presets.ValidateBase(b); err == nil {
		t.Error("MESTRE_* in a shell was accepted")
	}
}

func TestASheetIsCheckedAgainstItsBaseAndAgent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	ctx := context.Background()
	zero := 0
	tests := []struct {
		name   string
		sheet  presets.Sheet
		reason string
	}{
		{"a base that does not exist", presets.Sheet{Name: "A", Base: "nada", Agent: "claude-code"}, "a base `nada` não existe"},
		{"an agent that does not exist", presets.Sheet{Name: "A", Base: "claude-code", Agent: "gemini"}, "`agent` aceita"},
		{"a shell on a hook base", presets.Sheet{Name: "A", Base: "claude-code", Agent: "shell"}, "espera o hook"},
		{"another agent than the base starts", presets.Sheet{Name: "A", Base: "codex", Agent: "claude-code"}, "inicia o codex"},
		{"a name with spaces at the ends", presets.Sheet{Name: " A", Base: "claude-code", Agent: "claude-code"}, "de 1 a 80 caracteres"},
		{"a lifecycle that does not exist", presets.Sheet{Name: "A", Base: "claude-code", Agent: "claude-code", Lifecycle: "forever"}, "`lifecycle`"},
		{"an idle time that is not a duration", presets.Sheet{Name: "A", Base: "claude-code", Agent: "claude-code", MaxIdle: "muito"}, "`max_idle`"},
		{"no copies", presets.Sheet{Name: "A", Base: "claude-code", Agent: "claude-code", MaxCopies: &zero}, "`max_copies`"},
		{"an account on a shell", presets.Sheet{Name: "A", Base: "bash", Agent: "shell", Account: "x"}, "`account` não vale"},
		{"an account name that is a path", presets.Sheet{Name: "A", Base: "claude-code", Agent: "claude-code", Account: "../x"}, "nome de perfil inválido"},
		{"a provider that is not the agent", presets.Sheet{Name: "A", Base: "claude-code", Agent: "claude-code", Requires: map[string]any{"provider": "codex"}}, "não combina com o agente"},
		{"a system requirement", presets.Sheet{Name: "A", Base: "bash", Agent: "shell", Requires: map[string]any{"os": "linux"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.cat.SaveSheet(ctx, tt.sheet)
			if tt.reason == "" { // valid
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("err = %v; want a refusal with %q", err, tt.reason)
			}
		})
	}
}

func TestADefaultSheetHasTheDocumentedDefaults(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	s, _, _ := f.cat.Sheet(context.Background(), "Revisor Codex")
	if !s.HasSkills() || s.LifecycleOrDefault() != presets.Ephemeral || s.Copies() != 3 || s.Idle() != presets.DefaultMaxIdle {
		t.Errorf("sheet = %+v", s)
	}
	dev, _, _ := f.cat.Sheet(context.Background(), "Dev Claude")
	if dev.LifecycleOrDefault() != presets.Persistent || dev.Idle().Minutes() != 240 {
		t.Errorf("dev = %+v", dev)
	}
}

func TestDeletingABaseOrASheetInUseIsRefusedWithTheListOfWhoUsesIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	ctx := context.Background()
	err := f.cat.DeleteBase(ctx, "claude-code")
	var use *presets.InUseError
	if !errors.As(err, &use) || !errors.Is(err, presets.ErrInUse) || len(use.By) != 1 || use.By[0] != "`Dev Claude`" {
		t.Fatalf("base in use: %v", err)
	}
	f.users.set("Dev Claude", "dev-claude-1", "dev-claude-2")
	err = f.cat.DeleteSheet(ctx, "Dev Claude")
	if !errors.As(err, &use) || len(use.By) != 2 || !strings.Contains(err.Error(), "dev-claude-2") {
		t.Fatalf("sheet in use: %v", err)
	}
	f.users.set("Dev Claude")
	if err := f.cat.DeleteSheet(ctx, "Dev Claude"); err != nil {
		t.Fatal(err)
	}
	if err := f.cat.DeleteBase(ctx, "claude-code"); err != nil {
		t.Errorf("a base nobody uses: %v", err)
	}
	if err := f.cat.DeleteBase(ctx, "claude-code"); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("a base that is gone: %v", err)
	}
}

func TestEditingABaseThatWouldBreakItsSheetsIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	ctx := context.Background()
	if _, err := f.cat.SaveSheet(ctx, presets.Sheet{Name: "Shell", Base: "bash", Agent: "shell"}); err != nil {
		t.Fatal(err)
	}
	bash, _, _ := f.cat.Base(ctx, "bash")
	bash.Ready = presets.Ready{Mode: presets.ReadyHook} // a shell sheet cannot wait for a hook
	err := f.cat.SaveBase(ctx, bash)
	if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "a ficha `Shell` ficaria inválida") {
		t.Fatalf("err = %v", err)
	}
	if got, _, _ := f.cat.Base(ctx, "bash"); got.Ready.Mode != presets.ReadyPattern {
		t.Error("the base was changed")
	}
}

func TestACharacterKeepsTheCopyOfTheSheetAndBaseItWasBornFrom(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	ctx := context.Background()
	snap, err := f.cat.Snapshot(ctx, "Dev Claude")
	if err != nil {
		t.Fatal(err)
	}
	base, _, _ := f.cat.Base(ctx, "claude-code")
	base.Args = []string{"--model", "opus"}
	if err := f.cat.SaveBase(ctx, base); err != nil {
		t.Fatal(err)
	}
	if len(snap.Base.Args) != 0 {
		t.Errorf("the copy followed the edit: %v", snap.Base.Args)
	}
	again, _ := f.cat.Snapshot(ctx, "Dev Claude")
	if len(again.Base.Args) != 2 {
		t.Errorf("a new character: %v", again.Base.Args)
	}
	if _, err := f.cat.Snapshot(ctx, "Nada"); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("a sheet that is gone: %v", err)
	}
}

func TestTheStartFolderAndTheShellOfARunningBase(t *testing.T) {
	t.Parallel()
	b := presets.Base{Name: "bash", Command: "bash", Args: []string{"-l"}, Platforms: []presets.Platform{{OS: presets.Linux}},
		Ready: presets.Ready{Mode: presets.ReadyPattern, Pattern: `\$ $`}, Cwd: "{workspace}/ferramentas", Env: map[string]string{"LANG": "C", "A": "1"}}
	r, err := b.For(presets.Linux)
	if err != nil {
		t.Fatal(err)
	}
	v := presets.Vars{Mesa: "/m", Workspace: "/ws", Home: "/h"}
	got, err := r.Launch(v)
	if err != nil || got.Dir != "/ws/ferramentas" || got.Command != "bash" || strings.Join(got.Env, ",") != "A=1,LANG=C" {
		t.Fatalf("launch = %+v, %v", got, err)
	}
	r.Cwd = ""
	if d, _ := r.Dir(v); d != "/m" {
		t.Errorf("default folder = %q", d)
	}
	if _, err := r.Dir(presets.Vars{}); err == nil || !strings.Contains(err.Error(), "não tem valor") {
		t.Errorf("a variable without a value: %v", err)
	}
	// inside a shell: each word quoted for it
	for shell, want := range map[string][]string{
		"bash": {"-c", `'cat' 'a b' 'it'\''s'`},
		"pwsh": {"-NoProfile", "-Command", `& 'cat' 'a b' 'it''s'`},
		"cmd":  {"/d", "/s", "/c", `cat "a b" it's`},
	} {
		r := presets.Resolved{Name: "x", Command: "cat", Args: []string{"a b", "it's"}, Shell: shell}
		l, err := r.Launch(v)
		if err != nil || l.Command != shell || strings.Join(l.Args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s: %q %q, %v; want %q", shell, l.Command, l.Args, err, want)
		}
	}
}

func TestAnImportIsCheckedWholeAndAppliedAllOrNothing(t *testing.T) {
	t.Parallel()
	const good = `kind: mestre/presets
version: 1
bases:
  - {name: ok, command: bash, platforms: [{os: linux}], ready: {pattern: '\$ $'}}
  - {name: bad, command: bash, platforms: [], ready: {pattern: '\$ $'}}
sheets:
  - {name: Uma, base: ok, agent: shell}
  - {name: Duas, base: nao-existe, agent: shell}
`
	f := newFixture(t)
	if _, err := f.importFile(good, nil); !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "nenhuma plataforma") || !strings.Contains(err.Error(), "nao-existe") {
		t.Fatalf("err = %v; want both problems at once", err)
	}
	if b, s := snapshot(t, f); len(b) != 0 || len(s) != 0 {
		t.Errorf("something was written: %v %v", b, s)
	}
	for name, data := range map[string]string{
		"a newer version":  strings.Replace(example, "version: 1", "version: 2", 1),
		"another kind":     strings.Replace(example, "mestre/presets", "outro", 1),
		"an unknown field": strings.Replace(example, "ready: hook", "ready: hook\n    cor: azul", 1),
		"not a file":       "- 1\n- 2\n",
	} {
		_, err := f.importFile(data, nil)
		if !errors.Is(err, presets.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
		if name == "a newer version" && !strings.Contains(err.Error(), "até a versão 1") {
			t.Errorf("the version expected is not said: %v", err)
		}
	}
}

func TestAnImportAsksWhatToDoWithEachNameThatExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.mustImportExample()
	doc, err := presets.ParseDocument([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.cat.PlanImport(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != len(doc.Bases)+len(doc.Sheets) {
		t.Fatalf("conflicts = %v", plan.Conflicts)
	}
	if _, err := f.cat.ApplyImport(ctx, plan, nil); !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "escolha substituir, renomear ou pular") {
		t.Fatalf("without a choice: %v", err)
	}
	// rename the base codex: the sheet of the file that names it follows it;
	// replace one sheet, skip the rest
	choices := map[presets.Item]presets.Choice{}
	for _, c := range plan.Conflicts {
		choices[c] = presets.Choice{Action: presets.Skip}
	}
	choices[presets.Item{Kind: "base", Name: "codex"}] = presets.Choice{Action: presets.Rename, NewName: "codex-2"}
	choices[presets.Item{Kind: "sheet", Name: "Revisor Codex"}] = presets.Choice{Action: presets.Rename, NewName: "Revisor Codex 2"}
	choices[presets.Item{Kind: "sheet", Name: "Dev Claude"}] = presets.Choice{Action: presets.Replace}
	res, err := f.cat.ApplyImport(ctx, plan, choices)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 2 || res.Replaced != 1 || res.Skipped != len(plan.Conflicts)-3 {
		t.Errorf("applied = %+v", res)
	}
	s, ok, _ := f.cat.Sheet(ctx, "Revisor Codex 2")
	if !ok || s.Base != "codex-2" {
		t.Errorf("renamed sheet = %+v", s)
	}
	if _, ok, _ := f.cat.Base(ctx, "codex-2"); !ok {
		t.Error("the renamed base is missing")
	}
	// a new name that is taken, and a name that does not exist
	choices[presets.Item{Kind: "sheet", Name: "Dev Claude"}] = presets.Choice{Action: presets.Rename, NewName: "Dev Claude"}
	if _, err := f.cat.ApplyImport(ctx, plan, choices); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("rename to a taken name: %v", err)
	}
	choices[presets.Item{Kind: "sheet", Name: "Dev Claude"}] = presets.Choice{Action: "merge"}
	if _, err := f.cat.ApplyImport(ctx, plan, choices); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("an unknown action: %v", err)
	}
}

func TestExportingASelectionBringsTheBasesOfTheSheetsAndNeverTheOperatorBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.mustImportExample()
	if err := f.cat.SeedOperator(ctx); err != nil {
		t.Fatal(err)
	}
	doc, err := f.cat.Export(ctx, presets.Selection{Sheets: []string{"QA Antigravity"}})
	if err != nil || len(doc.Sheets) != 1 || len(doc.Bases) != 1 || doc.Bases[0].Name != "antigravity" {
		t.Fatalf("selection: %+v, %v", doc, err)
	}
	all, err := f.cat.Export(ctx, presets.Selection{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all.Bases {
		if b.Name == presets.OperatorBase {
			t.Error("the base the Mestre creates was exported")
		}
	}
	if _, err := f.cat.Export(ctx, presets.Selection{Sheets: []string{"Nada"}}); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("a sheet that does not exist: %v", err)
	}
	out, err := all.YAML()
	if err != nil || !strings.Contains(string(out), "kind: mestre/presets") || strings.Contains(string(out), "8.58") {
		t.Errorf("yaml: %s, %v", out, err)
	}
}
