package presets_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
)

// CA-BF-01
func TestTheBaseResolvesItsCommandForTheSystemOfTheMachine(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	base, ok, err := f.cat.Base(context.Background(), "claude-code")
	if err != nil || !ok {
		t.Fatalf("base: %v, %v", ok, err)
	}
	for os, want := range map[string]string{presets.Windows: "claude.exe", presets.Linux: "claude"} {
		r, err := base.For(os)
		if err != nil || r.Command != want {
			t.Errorf("%s: command %q, %v; want %q", os, r.Command, err, want)
		}
	}
}

func TestABaseDoesNotRunOnASystemItDoesNotList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	bash, _, _ := f.cat.Base(context.Background(), "bash")
	if _, err := bash.For(presets.Windows); err == nil || !strings.Contains(err.Error(), "não roda em windows") {
		t.Errorf("err = %v", err)
	}
}

const devClass = `
version: 1
commands:
  deny: ["rm -rf *", "curl *"]
tools:
  deny: ["mcp__evil__*"]
git:
  commit: false
`

// CA-BF-02
func TestRestrictionsThatReleaseWhatTheClassForbidsAreRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	f.classes.add(t, "Dev", devClass)
	ctx := context.Background()
	sheet := func(restrictions map[string]any) presets.Sheet {
		return presets.Sheet{Name: "Dev Junior", Base: "claude-code", Agent: "claude-code", Class: "Dev", Restrictions: restrictions}
	}
	for name, r := range map[string]map[string]any{
		"a command the class denies": {"version": 1, "commands": map[string]any{"allow": []any{"rm -rf *"}}},
		"a command inside a deny":    {"version": 1, "commands": map[string]any{"allow": []any{"curl https://example.com"}}},
		"a tool the class denies":    {"version": 1, "tools": map[string]any{"allow": []any{"mcp__evil__run"}}},
		"a switch the class is off":  {"version": 1, "git": map[string]any{"commit": true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.cat.SaveSheet(ctx, sheet(r))
			if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "restrictions") || !strings.Contains(err.Error(), "a ficha só restringe") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, ok, _ := f.cat.Sheet(ctx, "Dev Junior"); ok {
		t.Error("a refused sheet was saved")
	}
}

func TestRestrictionsThatOnlyRestrictAreSavedAndAnAllowTheClassDoesNotGiveWarns(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	f.classes.add(t, "Dev", "version: 1\ncommands:\n  allow: [\"git status\"]\n")
	ctx := context.Background()
	s := presets.Sheet{Name: "Dev Junior", Base: "claude-code", Agent: "claude-code", Class: "Dev",
		Restrictions: map[string]any{"version": 1, "commands": map[string]any{"deny": []any{"git commit *"}, "allow": []any{"npm test"}}}}
	warnings, err := f.cat.SaveSheet(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "commands.allow `npm test`") {
		t.Errorf("warnings = %q", warnings)
	}
	// the format of permissions.yaml is the sheet's too: a typo is refused
	s.Name, s.Restrictions = "Typo", map[string]any{"version": 1, "comands": map[string]any{}}
	if _, err := f.cat.SaveSheet(ctx, s); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("a field outside the format: %v", err)
	}
}

// snapshot is every base and sheet, as the catalog lists them.
func snapshot(t *testing.T, f *fixture) ([]presets.Base, []presets.Sheet) {
	t.Helper()
	b, err := f.cat.Bases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.cat.Sheets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b, s
}

// CA-BF-03
func TestAnExportedFileImportsAsJSONIntoAnEmptyDatabaseAndSkippingChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := newFixture(t)
	src.mustImportExample()
	doc, err := src.cat.Export(ctx, presets.Selection{All: true})
	if err != nil {
		t.Fatal(err)
	}
	yamlFile, err := doc.YAML()
	if err != nil {
		t.Fatal(err)
	}
	// the YAML read back is the same file; its JSON equivalent is what the
	// empty database imports
	again, err := presets.ParseDocument(yamlFile)
	if err != nil {
		t.Fatal(err)
	}
	jsonFile, err := again.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(jsonFile)), "{") {
		t.Fatalf("not JSON: %.40s", jsonFile)
	}
	dst := newFixture(t)
	if _, err := dst.importFile(string(jsonFile), nil); err != nil {
		t.Fatal(err)
	}
	sb, ss := snapshot(t, src)
	db, ds := snapshot(t, dst)
	if !reflect.DeepEqual(sb, db) || !reflect.DeepEqual(ss, ds) {
		t.Fatalf("bases and sheets differ:\nsrc %+v\n%+v\ndst %+v\n%+v", sb, ss, db, ds)
	}

	// again, skipping every name that exists: nothing changes, nothing is
	// written (the replication log does not grow)
	logBefore := logRows(t, dst)
	dst.clk.Advance(time.Hour)
	skip := map[presets.Item]presets.Choice{}
	for _, b := range doc.Bases {
		skip[presets.Item{Kind: "base", Name: b.Name}] = presets.Choice{Action: presets.Skip}
	}
	for _, s := range doc.Sheets {
		skip[presets.Item{Kind: "sheet", Name: s.Name}] = presets.Choice{Action: presets.Skip}
	}
	res, err := dst.importFile(string(jsonFile), skip)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != len(doc.Bases)+len(doc.Sheets) || res.Added+res.Replaced+res.Renamed != 0 {
		t.Errorf("applied = %+v", res)
	}
	db2, ds2 := snapshot(t, dst)
	if !reflect.DeepEqual(db, db2) || !reflect.DeepEqual(ds, ds2) {
		t.Error("skipping changed the bases or the sheets")
	}
	if got := logRows(t, dst); got != logBefore {
		t.Errorf("replication log: %d rows, was %d", got, logBefore)
	}
}

func logRows(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.db.Reader().QueryRow(`SELECT COUNT(*) FROM replication_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// CA-BF-04
func TestABaseThatSetsAMestreVariableIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	base := func(name string) presets.Base {
		return presets.Base{Name: "mine", Command: "claude", Platforms: []presets.Platform{{OS: presets.Linux}},
			Ready: presets.Ready{Mode: presets.ReadyHook}, Env: map[string]string{name: "x"}}
	}
	for _, name := range []string{"MESTRE_AGENT_ID", "mestre_agent_id"} {
		err := f.cat.SaveBase(ctx, base(name))
		if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, ok, _ := f.cat.Base(ctx, "mine"); ok {
		t.Error("a refused base was saved")
	}
	// a plain variable passes
	if err := f.cat.SaveBase(ctx, base("LANG")); err != nil {
		t.Errorf("LANG: %v", err)
	}
}

// CA-BF-05
func TestOnlyTheMachineWithTheAccountProfileMeetsTheSheet(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	profile := func(name string) machinesheet.Provider {
		return machinesheet.Provider{Provider: machinesheet.ProviderAntigravity, Profiles: []machinesheet.Profile{
			{Name: name, Version: "1.3.5", LoggedIn: true, Conformant: true, Default: true}}}
	}
	linux := sheetfake.Linux("linux-pc")
	linux.SetProviders(profile("agy-pessoal"))
	windows := sheetfake.Windows("windows-pc")
	windows.SetProviders(profile("agy-trabalho"))
	f.machines.set(onlineSheet(t, linux.Sources(), "m1", "linux-pc", true), onlineSheet(t, windows.Sources(), "m2", "windows-pc", true))

	choice, err := f.cat.Choose(context.Background(), "QA Antigravity", "")
	if err != nil {
		t.Fatal(err)
	}
	fit := choice.Fit()
	if len(fit) != 1 || fit[0].MachineID != "m1" {
		t.Fatalf("fit = %+v", fit)
	}
	if got := choice.Verdicts[1].String(); got != "windows-pc: perfil de conta `agy-pessoal` não existe" {
		t.Errorf("the other machine: %q", got)
	}
	if err := choice.Err(); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestTheBasePlatformsAndMachinesRestrictTheChoice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	ctx := context.Background()
	linux, windows := sheetfake.Linux("linux-pc"), sheetfake.Windows("windows-pc")
	f.machines.set(onlineSheet(t, linux.Sources(), "m1", "linux-pc", true), onlineSheet(t, windows.Sources(), "m2", "windows-pc", true),
		onlineSheet(t, linux.Sources(), "m3", "linux-off", false))
	if _, err := f.cat.SaveSheet(ctx, presets.Sheet{Name: "Shell", Base: "bash", Agent: "shell"}); err != nil {
		t.Fatal(err)
	}
	c, err := f.cat.Choose(ctx, "Shell", "")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{c.Verdicts[0].String(), c.Verdicts[1].String(), c.Verdicts[2].String()}
	want := []string{"linux-pc", "windows-pc: a base `bash` não roda em windows", "linux-off: offline"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("verdicts = %q, want %q", got, want)
	}
	// a base limited to some machines
	base, _, _ := f.cat.Base(ctx, "bash")
	base.Machines = []string{"m9"}
	if err := f.cat.SaveBase(ctx, base); err != nil {
		t.Fatal(err)
	}
	c, _ = f.cat.Choose(ctx, "Shell", "")
	if v := c.Verdicts[0]; v.OK || v.Unmet != "machines" {
		t.Errorf("verdict = %+v", v)
	}
}

// CA-BF-10
func TestTheInterfaceRequirementIsRefusedExceptInTheOperatorBase(t *testing.T) {
	t.Parallel()
	gui := map[string]any{"gui": true}
	painter := presets.Base{Name: "pintor", Command: "gimp", Platforms: []presets.Platform{{OS: presets.Windows}},
		Ready: presets.Ready{Mode: presets.ReadyDelay, Delay: 5}, Requires: gui}
	if err := presets.ValidateBase(painter); !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "só vale na base `operador`") {
		t.Errorf("pintor: err = %v", err)
	}
	op := presets.OperatorBaseDef()
	op.Requires = map[string]any{"operador": true, "gui": true}
	if err := presets.ValidateBase(op); err != nil {
		t.Errorf("operador: %v", err)
	}
	// and in a sheet that is not the Operator's
	f := newFixture(t)
	f.mustImportExample()
	_, err := f.cat.SaveSheet(context.Background(), presets.Sheet{Name: "Gui", Base: "claude-code", Agent: "claude-code", Requires: gui})
	if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "só vale na base `operador`") {
		t.Errorf("sheet: err = %v", err)
	}
}

// Part of CA-BF-06: the refusals; the criterion closes in milestone 7 with
// the Operator of the PC.
func TestChromeOutsideTheOperatorBaseAndANonOperatorClassOnItAreRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustImportExample()
	f.classes.add(t, "Dev", "version: 1\n")
	f.classes.add(t, "Piloto", "version: 1\noperator: true\n")
	ctx := context.Background()
	dev := presets.Base{Name: "claude-dev", Command: "claude", Args: []string{"--chrome"}, Platforms: []presets.Platform{{OS: presets.Linux}},
		Ready: presets.Ready{Mode: presets.ReadyHook}}
	if err := f.cat.SaveBase(ctx, dev); !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "`--chrome` só vale na base `operador`") {
		t.Errorf("base: err = %v", err)
	}
	if err := f.cat.SeedOperator(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := f.cat.SaveSheet(ctx, presets.Sheet{Name: "Operador Dev", Base: "operador", Agent: "claude-code", Class: "Dev"})
	if !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "não tem `operator: true`") {
		t.Errorf("sheet: err = %v", err)
	}
	if _, err := f.cat.SaveSheet(ctx, presets.Sheet{Name: "Operador Piloto", Base: "operador", Agent: "claude-code", Class: "Piloto"}); err != nil {
		t.Errorf("the class with operator: true: %v", err)
	}
}

// Part of CA-BF-09: the Operator's request itself (`mestre recruit`) comes
// with the Operator of the PC, in milestone 7.
func TestTheOperatorSheetIsIncompleteUntilAClassWithOperatorIsPicked(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if err := f.cat.SeedOperator(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.cat.SeedOperator(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	// a new installation has no class at all
	for _, name := range []string{"Dev", "Operador"} {
		if _, ok, _ := f.classes.Class(ctx, name); ok {
			t.Errorf("class %s exists", name)
		}
	}
	base, ok, _ := f.cat.Base(ctx, presets.OperatorBase)
	want := presets.OperatorBaseDef()
	if !ok || !reflect.DeepEqual(base, want) {
		t.Fatalf("base = %+v, want %+v", base, want)
	}
	s, ok, _ := f.cat.Sheet(ctx, presets.OperatorSheet)
	if !ok || s.Incomplete() == "" || s.Class != "" || s.Copies() != 1 || s.LifecycleOrDefault() != presets.Ephemeral {
		t.Fatalf("sheet = %+v (incomplete %q)", s, s.Incomplete())
	}
	if err := f.cat.CheckOperatorRequest(ctx, presets.OperatorSheet); !errors.Is(err, presets.ErrOperatorClassMissing) || !strings.Contains(err.Error(), "operator_class_missing") {
		t.Fatalf("request: %v", err)
	}
	// the Owner creates the class and picks it
	f.classes.add(t, "Piloto", "version: 1\noperator: true\n")
	s.Class = "Piloto"
	if _, err := f.cat.SaveSheet(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := f.cat.CheckOperatorRequest(ctx, presets.OperatorSheet); err != nil {
		t.Errorf("complete sheet: %v", err)
	}
	// the class loses the permission: the request fails again
	f.classes.add(t, "Piloto", "version: 1\n")
	if err := f.cat.CheckOperatorRequest(ctx, presets.OperatorSheet); !errors.Is(err, presets.ErrOperatorClassMissing) {
		t.Errorf("class without operator: %v", err)
	}
}

func TestTheOperatorBaseIsCreatedByTheMestreAndOnlyItsArgumentsChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	fake := presets.OperatorBaseDef()
	if err := f.cat.SaveBase(ctx, fake); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("creating it by hand: %v", err)
	}
	if err := f.cat.SeedOperator(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.cat.DeleteBase(ctx, presets.OperatorBase); !errors.Is(err, presets.ErrBuiltin) {
		t.Errorf("deleting it: %v", err)
	}
	b := presets.OperatorBaseDef()
	b.Args = []string{"--chrome", "--verbose"}
	if err := f.cat.SaveBase(ctx, b); err != nil {
		t.Errorf("the arguments: %v", err)
	}
	b.Command = "other.exe"
	if err := f.cat.SaveBase(ctx, b); !errors.Is(err, presets.ErrInvalid) || !strings.Contains(err.Error(), "só os argumentos se editam") {
		t.Errorf("the command: %v", err)
	}
	b = presets.OperatorBaseDef()
	b.Args = []string{"--dangerously-skip-permissions"}
	if err := f.cat.SaveBase(ctx, b); !errors.Is(err, presets.ErrInvalid) {
		t.Errorf("a forbidden flag: %v", err)
	}
}
