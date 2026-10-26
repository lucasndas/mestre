package accounts_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

var machineTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "accounts-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "accounts:", err)
		os.Exit(1)
	}
	path := filepath.Join(dir, "maquina.db")
	db, err := guildtpl.OpenMachine(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err == nil {
		err = db.Close()
	}
	if err == nil {
		machineTemplate, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "accounts: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type folders struct {
	mu   sync.Mutex
	made []string
}

func (f *folders) Ensure(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made = append(f.made, dir)
	return nil
}

type fixture struct {
	reg     *accounts.Registry
	folders *folders
	clk     *clock.Fake
}

func newFixture(t *testing.T, osName string) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maquina.db")
	if err := os.WriteFile(path, machineTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.OpenMachine(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{folders: &folders{}, clk: clock.NewFake(day0)}
	f.reg = &accounts.Registry{DB: db, Clock: f.clk, Root: filepath.Join("var", "lib", "mestre", "contas"), OS: osName, Folders: f.folders}
	return f
}

func TestAProfileIsRegisteredWithItsFolderAndTheFirstOneIsTheDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	p, err := f.reg.Add(ctx, "claude-trabalho", providers.ClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("var", "lib", "mestre", "contas", "claude-trabalho")
	if p.Dir != want || !p.Default || p.Step != accounts.StepInstall || len(f.folders.made) != 1 || f.folders.made[0] != want {
		t.Errorf("profile = %+v, folders %v", p, f.folders.made)
	}
	if env := p.Env(); len(env) != 1 || env[0] != "CLAUDE_CONFIG_DIR="+want {
		t.Errorf("env = %v", env)
	}
	second, err := f.reg.Add(ctx, "claude-pessoal", providers.ClaudeCode)
	if err != nil || second.Default {
		t.Fatalf("second = %+v, %v", second, err)
	}
	for _, name := range []string{"../fora", "Claude", "", "a/b"} {
		if _, err := f.reg.Add(ctx, name, providers.Codex); !errors.Is(err, accounts.ErrName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if _, err := f.reg.Add(ctx, "claude-pessoal", providers.ClaudeCode); !errors.Is(err, accounts.ErrExists) {
		t.Errorf("the same name: %v", err)
	}
	if _, err := f.reg.Add(ctx, "gemini", "gemini"); err == nil {
		t.Error("an unknown provider")
	}
}

func TestTheProfileASheetUsesIsTheOneNamedOrTheDefaultOfTheProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	for _, n := range []string{"codex-a", "codex-b"} {
		if _, err := f.reg.Add(ctx, n, providers.Codex); err != nil {
			t.Fatal(err)
		}
	}
	if p, err := f.reg.Resolve(ctx, providers.Codex, "codex-b"); err != nil || p.Name != "codex-b" {
		t.Errorf("named: %+v, %v", p, err)
	}
	if p, err := f.reg.Resolve(ctx, providers.Codex, ""); err != nil || p.Name != "codex-a" {
		t.Errorf("default: %+v, %v", p, err)
	}
	if _, err := f.reg.Resolve(ctx, providers.Codex, "codex-c"); !errors.Is(err, accounts.ErrNoProfile) {
		t.Errorf("a profile the machine lacks: %v", err)
	}
	// the profile of another provider is not served by that name
	if _, err := f.reg.Resolve(ctx, providers.ClaudeCode, "codex-a"); !errors.Is(err, accounts.ErrNoProfile) {
		t.Errorf("another provider: %v", err)
	}
	if err := f.reg.SetDefault(ctx, "codex-b"); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.reg.Resolve(ctx, providers.Codex, ""); p.Name != "codex-b" {
		t.Errorf("after SetDefault: %+v", p)
	}
	// with the default gone and two left, no profile is the default
	if err := f.reg.Remove(ctx, "codex-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.Add(ctx, "codex-c", providers.Codex); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.Resolve(ctx, providers.Codex, ""); !errors.Is(err, accounts.ErrNoProfile) {
		t.Errorf("no default among two: %v", err)
	}
	if err := f.reg.Remove(ctx, "nada"); !errors.Is(err, accounts.ErrUnknown) {
		t.Errorf("removing what is not there: %v", err)
	}
}

func TestTheCodexAlwaysHasAProfileOfTheMestre(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	p, err := f.reg.EnsureDefault(ctx, providers.Codex)
	if err != nil || p.Name != "codex-padrao" || !p.Default {
		t.Fatalf("created: %+v, %v", p, err)
	}
	again, err := f.reg.EnsureDefault(ctx, providers.Codex)
	if err != nil || again.Name != "codex-padrao" || len(f.folders.made) != 1 {
		t.Errorf("again: %+v, %v, folders %v", again, err, f.folders.made)
	}
	if got, err := f.reg.Resolve(ctx, providers.Codex, ""); err != nil || got.Name != "codex-padrao" {
		t.Errorf("a sheet without account: %+v, %v", got, err)
	}
}

func TestAntigravityHasOneProfileOnAWindowsMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, osName := range []string{"windows", "linux"} {
		f := newFixture(t, osName)
		if _, err := f.reg.Add(ctx, "agy-pessoal", providers.Antigravity); err != nil {
			t.Fatal(err)
		}
		_, err := f.reg.Add(ctx, "agy-trabalho", providers.Antigravity)
		if (osName == "windows") != errors.Is(err, accounts.ErrExists) {
			t.Errorf("%s: second profile: %v", osName, err)
		}
	}
}

// fakeSteps is the machine of the setup: what is installed, who is logged in,
// and what the Owner has done in the terminals.
type fakeSteps struct {
	mu          sync.Mutex
	version     map[string]string
	loggedIn    map[string]bool
	trusted     map[string]bool
	installs    []string
	terminals   []string
	conformant  bool
	reason      string
	installFail error
}

func newSteps() *fakeSteps {
	return &fakeSteps{version: map[string]string{}, loggedIn: map[string]bool{}, trusted: map[string]bool{}, conformant: true}
}

func (s *fakeSteps) CLIVersion(_ context.Context, p accounts.Profile) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version[p.Name], nil
}

func (s *fakeSteps) InstallCLI(_ context.Context, p accounts.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installFail != nil {
		return s.installFail
	}
	s.installs = append(s.installs, p.Name)
	s.version[p.Name] = "0.162.1"
	return nil
}

func (s *fakeSteps) LoggedIn(_ context.Context, p accounts.Profile) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loggedIn[p.Name], nil
}

func (s *fakeSteps) OpenTerminal(_ context.Context, p accounts.Profile, hooks bool) (accounts.Terminal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := "login"
	if hooks {
		kind = "hooks"
	}
	s.terminals = append(s.terminals, p.Name+":"+kind)
	return accounts.Terminal{ID: "t" + fmt.Sprint(len(s.terminals)), Hint: kind}, nil
}

func (s *fakeSteps) HooksTrusted(_ context.Context, p accounts.Profile) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trusted[p.Name], nil
}

func (s *fakeSteps) Conformance(context.Context, accounts.Profile, string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conformant, s.reason, nil
}

func (s *fakeSteps) with(f func(s *fakeSteps)) { s.mu.Lock(); f(s); s.mu.Unlock() }

func TestTheGuidedSetupInstallsLogsInApprovesTheCodexHooksAndRunsTheConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	steps := newSteps()
	setup := &accounts.Setup{Registry: f.reg, Steps: steps}
	if _, err := f.reg.Add(ctx, "codex-pessoal", providers.Codex); err != nil {
		t.Fatal(err)
	}

	// 0 and 1: the CLI is installed, then the login waits for the Owner
	st, err := setup.Advance(ctx, "codex-pessoal")
	if err != nil || st.Step != accounts.StepLogin || st.Terminal == nil || st.Done || len(steps.installs) != 1 {
		t.Fatalf("first: %+v, %v, installs %v", st, err, steps.installs)
	}
	// the same call again installs nothing and opens no second login... it
	// opens the terminal again, since the first may have been closed
	if st, _ = setup.Advance(ctx, "codex-pessoal"); st.Step != accounts.StepLogin || len(steps.installs) != 1 {
		t.Fatalf("again: %+v installs %v", st, steps.installs)
	}
	// 2: logged in, the hooks of the Codex wait for the Owner's approval
	steps.with(func(s *fakeSteps) { s.loggedIn["codex-pessoal"] = true })
	st, _ = setup.Advance(ctx, "codex-pessoal")
	if st.Step != accounts.StepHooks || st.Terminal == nil || st.Waiting == "" {
		t.Fatalf("hooks: %+v", st)
	}
	// 3: the conformance fails, and the profile stays there with the reason
	steps.with(func(s *fakeSteps) {
		s.trusted["codex-pessoal"] = true
		s.conformant = false
		s.reason = "etapa 2: falhou"
	})
	st, _ = setup.Advance(ctx, "codex-pessoal")
	if st.Step != accounts.StepConformance || st.Done || st.Reason != "etapa 2: falhou" {
		t.Fatalf("conformance: %+v", st)
	}
	if p, _, _ := f.reg.Profile(ctx, "codex-pessoal"); p.Step != accounts.StepConformance || p.Detail != "etapa 2: falhou" {
		t.Errorf("stored: %+v", p)
	}
	steps.with(func(s *fakeSteps) { s.conformant = true })
	f.clk.Advance(time.Minute)
	st, err = setup.Advance(ctx, "codex-pessoal")
	if err != nil || !st.Done || !st.Conformant {
		t.Fatalf("done: %+v, %v", st, err)
	}
	p, _, _ := f.reg.Profile(ctx, "codex-pessoal")
	if p.Step != accounts.StepDone || !p.SetupDoneAt.Equal(day0.Add(time.Minute)) {
		t.Errorf("stored: %+v", p)
	}
	// done stays done, and Redo takes it back to the start
	f.clk.Advance(time.Hour)
	if st, _ = setup.Advance(ctx, "codex-pessoal"); !st.Done {
		t.Errorf("after done: %+v", st)
	}
	if p, _, _ = f.reg.Profile(ctx, "codex-pessoal"); !p.SetupDoneAt.Equal(day0.Add(time.Minute)) {
		t.Errorf("done time moved: %v", p.SetupDoneAt)
	}
	if err := setup.Redo(ctx, "codex-pessoal"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ = f.reg.Profile(ctx, "codex-pessoal"); p.Step != accounts.StepInstall {
		t.Errorf("after redo: %+v", p)
	}
	if _, err := setup.Advance(ctx, "nada"); !errors.Is(err, accounts.ErrUnknown) {
		t.Errorf("unknown profile: %v", err)
	}
}

func TestOnlyTheCodexHasTheHooksStepAndAFailedInstallStops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	steps := newSteps()
	setup := &accounts.Setup{Registry: f.reg, Steps: steps}
	if _, err := f.reg.Add(ctx, "claude-trabalho", providers.ClaudeCode); err != nil {
		t.Fatal(err)
	}
	steps.with(func(s *fakeSteps) { s.version["claude-trabalho"] = "2.1.299"; s.loggedIn["claude-trabalho"] = true })
	if st, err := setup.Advance(ctx, "claude-trabalho"); err != nil || !st.Done || len(steps.terminals) != 0 || len(steps.installs) != 0 {
		t.Fatalf("claude: %+v, %v, terminals %v, installs %v", st, err, steps.terminals, steps.installs)
	}
	if _, err := f.reg.Add(ctx, "agy-pessoal", providers.Antigravity); err != nil {
		t.Fatal(err)
	}
	steps.with(func(s *fakeSteps) { s.installFail = errors.New("sem rede") })
	if st, err := setup.Advance(ctx, "agy-pessoal"); err == nil || st.Step != accounts.StepInstall {
		t.Errorf("failed install: %+v, %v", st, err)
	}
}

type cli struct{ steps *fakeSteps }

func (c cli) CLIVersion(ctx context.Context, p accounts.Profile) (string, error) {
	return c.steps.CLIVersion(ctx, p)
}
func (c cli) LoggedIn(ctx context.Context, p accounts.Profile) (bool, error) {
	return c.steps.LoggedIn(ctx, p)
}

func TestTheProbeTellsTheMachineSheetTheProvidersWithACLIAndTheStateOfEachProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t, "linux")
	steps := newSteps()
	for name, p := range map[string]providers.ID{"claude-trabalho": providers.ClaudeCode, "codex-pessoal": providers.Codex, "codex-outro": providers.Codex} {
		if _, err := f.reg.Add(ctx, name, p); err != nil {
			t.Fatal(err)
		}
	}
	steps.with(func(s *fakeSteps) {
		s.version["codex-pessoal"], s.loggedIn["codex-pessoal"] = "0.162.1", true
		s.version["codex-outro"] = "0.162.1"
		// claude-trabalho: no CLI yet
	})
	got, err := accounts.Probe{Registry: f.reg, CLI: cli{steps}}.Providers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Provider != "codex" || len(got[0].Profiles) != 2 {
		t.Fatalf("providers = %+v", got)
	}
	by := map[string]bool{}
	for _, pr := range got[0].Profiles {
		by[pr.Name] = pr.LoggedIn
		if pr.Conformant || pr.Version != "0.162.1" {
			t.Errorf("profile = %+v", pr)
		}
	}
	if !by["codex-pessoal"] || by["codex-outro"] {
		t.Errorf("logins = %v", by)
	}
}
