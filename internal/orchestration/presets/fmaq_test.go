package presets_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/conformance"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/testkit/sheetfake"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// profileCLI is the CLI of the profiles of a machine: versions and logins.
type profileCLI struct {
	version  string
	loggedIn map[string]bool
}

func (c profileCLI) CLIVersion(context.Context, accounts.Profile) (string, error) {
	return c.version, nil
}

func (c profileCLI) LoggedIn(_ context.Context, p accounts.Profile) (bool, error) {
	return c.loggedIn[p.Name], nil
}

// results is the conformance results of the machine, in memory.
type results struct {
	mu sync.Mutex
	r  map[string]conformance.Result
}

func (r *results) Save(_ context.Context, res conformance.Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.r[res.Profile] = res
	return nil
}

func (r *results) Latest(_ context.Context, _, profile string, _ providers.ID) (conformance.Result, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.r[profile]
	return res, ok, nil
}

// machineWithProfile is a Windows machine whose provider part of the sheet
// comes from the real registry of account profiles and the conformance
// results, as the engine builds it, not from the fake.
func machineWithProfile(t *testing.T, id string, loggedIn bool, conformant *bool) machinesheet.Sheet {
	t.Helper()
	ctx := context.Background()
	db, err := guildtpl.OpenMachine(ctx, filepath.Join(t.TempDir(), "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := &accounts.Registry{DB: db, Clock: clock.NewFake(day0), Root: filepath.Join(t.TempDir(), "contas"), OS: machinesheet.OSWindows, Folders: noFolders{}}
	if _, err := reg.Add(ctx, "codex-pessoal", providers.Codex); err != nil {
		t.Fatal(err)
	}
	res := &results{r: map[string]conformance.Result{}}
	if conformant != nil {
		res.r["codex-pessoal"] = conformance.Result{Machine: id, Profile: "codex-pessoal", Provider: providers.Codex, Version: "0.162.1", Conformant: *conformant}
	}
	machine := sheetfake.Windows(id)
	src := machine.Sources()
	src.Providers = conformance.Probe{
		Machine:   id,
		Installed: accounts.Probe{Registry: reg, CLI: profileCLI{version: "0.162.1", loggedIn: map[string]bool{"codex-pessoal": loggedIn}}},
		Results:   res,
	}
	return onlineSheet(t, src, id, id, true)
}

type noFolders struct{}

func (noFolders) Ensure(string) error { return nil }

// CA-FMAQ-03
func TestACodexSheetIsNotMetByAMachineWhoseProfileIsNotLoggedInOrNotConformant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	yes, no := true, false
	tests := []struct {
		name       string
		loggedIn   bool
		conformant *bool
		ok         bool
		want       string
	}{
		{"installed, not logged in", false, &yes, false, "m1: perfil de conta `codex-pessoal` não está logado"},
		{"logged in, not conformant", true, &no, false, "m1: perfil de conta `codex-pessoal` não é conforme na versão 0.162.1"},
		{"logged in, never tested", true, nil, false, "m1: perfil de conta `codex-pessoal` não é conforme na versão 0.162.1"},
		{"logged in and conformant", true, &yes, true, "m1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ff := newFixture(t)
			ff.mustImportExample()
			if _, err := ff.cat.SaveSheet(ctx, presets.Sheet{Name: "Codex pessoal", Base: "codex", Agent: "codex", Account: "codex-pessoal"}); err != nil {
				t.Fatal(err)
			}
			ff.machines.set(machineWithProfile(t, "m1", tt.loggedIn, tt.conformant))
			c, err := ff.cat.Choose(ctx, "Codex pessoal", "")
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Verdicts[0]; got.OK != tt.ok || got.String() != tt.want {
				t.Errorf("verdict = %q (ok %v), want %q", got, got.OK, tt.want)
			}
		})
	}
}
