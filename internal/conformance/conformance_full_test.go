//go:build full

package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/leakcheck"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite runs the conformance with the fake agent as the CLI of the
// provider, on real terminals (the mestre-pty), with the real `mestre hook`
// and the engine side of internal/conformance.
var builds struct{ pty, mestre, claude string }

const ptyVersion = "1.0.0"

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mcfbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	builds.pty = filepath.Join(dir, "pty", ptyVersion, "mestre-pty"+exe)
	builds.mestre = filepath.Join(dir, "bin", "mestre"+exe)
	builds.claude = filepath.Join(dir, "fake", "claude"+exe) // the fake agent called claude is the fake Claude Code
	for _, b := range [][]string{
		{"-o", builds.pty, "-ldflags", "-X main.version=" + ptyVersion, "github.com/lucasndas/mestre/cmd/mestre-pty"},
		{"-o", builds.mestre, "github.com/lucasndas/mestre/cmd/mestre"},
		{"-o", builds.claude, "github.com/lucasndas/mestre/cmd/fakeagent"},
	} {
		if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go build %v: %v\n%s", b, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	// A test that left a process of these binaries alive fails the run.
	code = leakcheck.Main(os.Stderr, code, dir)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// script is how the fake Claude Code answers the messages of the steps: with
// the codes and the skills it reads, as the real CLI would have them in its
// context.
const script = `prompts:
  - match: "CODIGOS"
    steps:
      - say: "CODIGOS: {{codes}}\nPALAVRA: VERDE SHELL: Bash"
  - match: "skill mestre-conformidade"
    steps:
      - say: "{{skill:mestre-conformidade}}"
  - match: "proibido/x.txt"
    steps:
      - edit: {path: proibido/x.txt, content: x}
      - say: negado
  - match: "permitido/y.txt"
    steps:
      - run: "echo ok > permitido/y.txt"
      - run: "git add permitido/y.txt"
      - run: "git commit -m conformidade"
      - run: "mestre debug"
      - say: feito
  - match: "git status"
    steps:
      - run: "git status"
      - say: ok
  - match: "mestre-sonda"
    steps:
      - say: "{{skill:mestre-sonda}}"
  - match: "subagente"
    steps:
      - tool: {name: Agent, input: {description: criar, prompt: "crie proibido/z.txt"}}
      - edit: {path: proibido/z.txt, content: z}
      - say: negado
`

// checklist records what the run tells the checklist.
type checklist struct {
	mu      sync.Mutex
	notices []conformance.Notice
}

func (c *checklist) Conformance(_ context.Context, n conformance.Notice) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notices = append(c.notices, n)
	return nil
}

func (c *checklist) all() []conformance.Notice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.notices)
}

// rig is one machine of the test: the engine side, the Guild database of
// the results and a Claude Code profile with a fake login.
type rig struct {
	t       *testing.T
	root    string
	results conformance.ResultsDB
	list    *checklist
	runner  *conformance.Runner
	profile string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx := t.Context()
	host, err := conformance.NewHost(ctx, conformance.HostConfig{Root: filepath.Join(root, "h"), PTYBinary: builds.pty, PTYVersion: ptyVersion,
		Suite: "full", Clock: clock.Real{}, Layout: classes.Layout{Data: []string{filepath.Join(root, "h")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	git, err := gitx.New(gitx.Options{Suite: "full", Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, root: root, results: conformance.ResultsDB{DB: db}, list: &checklist{}, profile: filepath.Join(root, "perfil-claude")}
	testkit.WriteFiles(t, root, map[string]string{"perfil-claude/.credentials.json": "{}", "script.yaml": script})
	if err := os.MkdirAll(filepath.Join(root, "ws"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.runner = &conformance.Runner{Clock: clock.Real{}, Git: git, Host: host, Results: r.results, Checklist: r.list,
		TempDir: filepath.Join(root, "ws"), Limit: 2 * time.Minute, ReadyLimit: 30 * time.Second, TurnLimit: 30 * time.Second}
	return r
}

// inheritedEnv is the environment of the test, which the base of the run
// carries, without what a base may not set (bases-e-fichas.md#regras-e-erros):
// the suite may itself run inside an agent, whose variables would refuse
// the start.
func inheritedEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		return slices.Contains([]string{"SHELL", "NODE_OPTIONS", "BASH_ENV", "ENV", "GIT_EXEC_PATH"}, k) ||
			slices.ContainsFunc([]string{"CLAUDE", "MESTRE_", "CODEX_", "LD_", "DYLD_", "GIT_CONFIG"}, func(p string) bool {
				return strings.HasPrefix(k, p)
			})
	})
}

// request is a run of the fake Claude Code on the profile of the rig.
func (r *rig) request(extraEnv ...string) conformance.Request {
	env := append(inheritedEnv(), "HOME="+r.profile, "FAKEAGENT_SCRIPT="+filepath.Join(r.root, "script.yaml"),
		// A fake built with -race would wait a second at each exit.
		"GORACE=atexit_sleep_ms=0")
	return conformance.Request{
		Machine: "m1", Provider: providers.ClaudeCode, Version: "2.1.296", Origin: conformance.OriginHeavy,
		Profile: conformance.Profile{Name: "claude-trabalho", Dir: r.profile},
		Base:    providers.Base{Name: "claude", Command: builds.claude, Env: append(env, extraEnv...)},
		Mestre:  builds.mestre,
	}
}

func (r *rig) run(req conformance.Request) conformance.Result {
	r.t.Helper()
	res, err := r.runner.Run(r.t.Context(), req)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

// installed is the provider part of the machine sheet as the probes read
// it: the CLI and the logged-in profile, of the version tested.
type installed struct{ version string }

func (i installed) Providers(context.Context) ([]machinesheet.Provider, error) {
	return []machinesheet.Provider{{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{
		{Name: "claude-trabalho", Version: i.version, LoggedIn: true, Conformant: true, Default: true}}}}, nil
}

// CA-CONF-02
func TestAProviderThatNeverFiresPreToolUseIsNotConformantAndTheMachineIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	res := r.run(r.request("FAKEAGENT_SKIP_HOOKS=PreToolUse"))

	if res.Conformant {
		t.Fatalf("the provider is conformant without PreToolUse: %+v", res)
	}
	if !slices.Contains(res.MissingEvents, string(providers.EventPreTool)) || res.Step(conformance.StepDeniedWrite).Outcome != conformance.Failed {
		t.Fatalf("missing %v, step 4 %+v; want pre_tool missing and the denial of step 4 failed", res.MissingEvents, res.Step(conformance.StepDeniedWrite))
	}
	saved, ok, err := r.results.Latest(t.Context(), "m1", "claude-trabalho", providers.ClaudeCode)
	if err != nil || !ok || saved.Conformant || saved.Version != "2.1.296" {
		t.Fatalf("saved %+v, %t, %v; want the result not conformant of version 2.1.296", saved, ok, err)
	}
	// The recruitment of a character of a claude-code base chooses the
	// machine by the provider requirement, which reads the result.
	probe := conformance.Probe{Machine: "m1", Installed: installed{"2.1.296"}, Results: r.results}
	ps, err := probe.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sheet := machinesheet.Sheet{MachineID: "m1", Name: "m1", Online: true, OS: runtime.GOOS, Providers: ps}
	req, err := machinesheet.Requirements{}.WithAgent(machinesheet.ProviderClaudeCode, "")
	if err != nil {
		t.Fatal(err)
	}
	err = machinesheet.Choose([]machinesheet.Sheet{sheet}, req).Err()
	if !errors.Is(err, machinesheet.ErrNoMachine) || !strings.Contains(err.Error(), "m1: perfil de conta `claude-trabalho` não é conforme na versão 2.1.296") {
		t.Fatalf("recruitment = %v; want the machine refused with the explanation", err)
	}
	if n := r.list.all(); len(n) == 0 || n[0].Kind != conformance.NoticeNotConformant {
		t.Fatalf("checklist %+v; want the alert of the provider not conformant", n)
	}
}

// CA-CONF-03
// Part of CA-CONF-06 (the half of the full suite: the CLI of the fake agent reaches the engine, step 5 passes)
func TestARequestForOneProviderTestsOnlyItAndReplacesItsPreviousResult(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := t.Context()
	old := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, prev := range []conformance.Result{
		{Machine: "m1", Profile: "claude-trabalho", Provider: providers.ClaudeCode, Version: "2.1.295", Origin: conformance.OriginProfileSetup,
			Conformant: false, Reason: "etapa 2: antes", StartedAt: old, FinishedAt: old},
		{Machine: "m1", Profile: "codex-pessoal", Provider: providers.Codex, Version: "0.162.0", Origin: conformance.OriginProfileSetup,
			Conformant: true, StartedAt: old, FinishedAt: old},
		{Machine: "m2", Profile: "claude-trabalho", Provider: providers.ClaudeCode, Version: "2.1.295", Origin: conformance.OriginProfileSetup,
			Conformant: true, StartedAt: old, FinishedAt: old},
	} {
		if err := r.results.Save(ctx, prev); err != nil {
			t.Fatal(err)
		}
	}
	codexProfile := filepath.Join(r.root, "perfil-codex")
	testkit.WriteFiles(t, r.root, map[string]string{"perfil-codex/auth.json": "{}"})

	// The update of the CLIs saw a new version of Claude Code in one profile
	// of m1 and asks for its conformance.
	req := r.request()
	req.Version, req.Origin = "2.1.296", conformance.OriginCLIUpdate
	res := r.run(req)
	if !res.Conformant {
		t.Fatalf("the fake Claude Code is not conformant: %s\n%+v", res.Reason, res.Steps)
	}

	if s5 := res.Step(conformance.StepCommandCommit); s5.Outcome != conformance.Passed || res.Flags.CLIUnreachable {
		t.Fatalf("step 5 = %+v, cli_unreachable %v: the CLI of the fake agent reaches the engine", s5, res.Flags.CLIUnreachable)
	}
	got, ok, err := r.results.Latest(ctx, "m1", "claude-trabalho", providers.ClaudeCode)
	if err != nil || !ok || got.Version != "2.1.296" || !got.Conformant || got.Origin != conformance.OriginCLIUpdate || len(got.Steps) != 12 {
		t.Fatalf("m1/claude-trabalho = %+v; want the new result in place of the previous one", got)
	}
	for _, other := range []struct {
		machine, profile string
		p                providers.ID
		version          string
	}{{"m1", "codex-pessoal", providers.Codex, "0.162.0"}, {"m2", "claude-trabalho", providers.ClaudeCode, "2.1.295"}} {
		o, ok, err := r.results.Latest(ctx, other.machine, other.profile, other.p)
		if err != nil || !ok || o.Version != other.version || !o.StartedAt.Equal(old) {
			t.Fatalf("%s/%s = %+v; want it untouched", other.machine, other.profile, o)
		}
	}
	// Only the requested profile was used: the Codex profile of the same
	// machine got no file.
	entries, err := os.ReadDir(codexProfile)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the Codex profile has %v (%v); want only its login", entries, err)
	}
}

// CA-CONF-04
func TestAClaudeThatReadsAgentsSkillsShowsTheManagedCopyCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	res := r.run(r.request("FAKEAGENT_CLAUDE_AGENTS_SKILLS=1"))

	if s := res.Step(conformance.StepProbeSkill); s.Outcome != conformance.Passed || !res.Flags.AgentsSkillsRead {
		t.Fatalf("step 9 %+v, flags %+v; want the probe skill seen", s, res.Flags)
	}
	want := "o Claude já lê .agents/skills/; a cópia gerenciada pode ser desligada"
	if !slices.ContainsFunc(r.list.all(), func(n conformance.Notice) bool {
		return n.Kind == conformance.NoticeInfo && n.Step == conformance.StepProbeSkill && n.Text == want && n.Provider == providers.ClaudeCode
	}) {
		t.Fatalf("checklist %+v; want %q", r.list.all(), want)
	}
}
