package classes_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

const agentID = "01JQAGENTQA"

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// machine is the folder layout of an installed instance, under a root that
// is absolute on both systems.
type machine struct {
	layout                                   classes.Layout
	base, wt, tmp, accounts, agents, outside string
}

// newMachine is the layout of an installed instance "principal"
// (classes.DefaultLayout) on this system.
func newMachine() machine {
	l := classes.DefaultLayout(runtime.GOOS, `C:\ProgramData`, "principal")
	base, outside := filepath.Dir(l.Accounts), filepath.FromSlash("/etc/passwd")
	if runtime.GOOS == "windows" {
		outside = `C:\Windows\win.ini`
	}
	return machine{
		layout: l, base: base, outside: outside,
		wt:       filepath.Join(l.Workspaces, "loja", "mesas", "42-login"),
		tmp:      filepath.Join(l.Agents, agentID, "tmp"),
		accounts: l.Accounts, agents: l.Agents,
	}
}

func (m machine) env() classes.Env {
	return classes.Env{
		AgentID: agentID, TableID: "42-login", Worktree: m.wt, Tmp: m.tmp,
		Layout: m.layout, Exceptions: newLedger(),
	}
}

// in is a path inside the worktree.
func (m machine) in(rel string) string { return filepath.Join(m.wt, filepath.FromSlash(rel)) }

// rules builds the effective rules of a class given as permissions.yaml.
func rules(t testing.TB, yaml string, decrees ...classes.Decree) *classes.Rules {
	t.Helper()
	p, err := classes.ParsePermissions([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := classes.Build(classes.Layers{
		AgentID: agentID, Class: "qa", Permissions: p, Decrees: decrees,
		Placeholders: map[string]string{"mesa.branch": "epico/42-login"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// qaYAML is the QA class of docs/classes/formato-das-permissoes.md.
const qaYAML = `version: 1
files:
  read: ["**"]
  write: ["tests/**", "reports/**"]
  deny: ["**/.env", "secrets/**"]
commands:
  allow: ["npm test", "npm run test:*", "git add *", "git commit *"]
  ask: ["npm install *"]
  deny: ["curl *", "rm -rf *"]
  default: ask
git:
  commit: true
  branches: ["{mesa.branch}"]
network:
  internet: false
tools:
  allow: ["mcp__playwright__*"]
  deny: ["mcp__*__delete_*"]
  default: ask
`

// anyYAML is a class that frees everything the class layer can free.
const anyYAML = `version: 1
files: {read: ["**"], write: ["**"]}
commands: {allow: ["*"], default: allow}
tools: {allow: ["*"], default: allow}
git: {commit: true, branches: ["**"], request_push: true, workflows: allow}
network: {internet: true}
operator: true
`

func event(tool *providers.Tool, cwd string) providers.Event {
	return providers.Event{
		Version: providers.EventVersion, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse",
		Event: providers.EventPreTool, AgentID: agentID, Cwd: cwd, Tool: tool, ReceivedAt: now,
	}
}

func (m machine) shell(cmd string) providers.Event {
	return event(&providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: cmd}, m.wt)
}

func (m machine) file(kind providers.ToolKind, paths ...string) providers.Event {
	name := map[providers.ToolKind]string{providers.KindRead: "Read", providers.KindWrite: "Write", providers.KindEdit: "Edit"}[kind]
	return event(&providers.Tool{NativeName: name, Kind: kind, Paths: paths}, m.wt)
}

func toolEvent(kind providers.ToolKind, name string) providers.Event {
	return event(&providers.Tool{NativeName: name, Kind: kind}, "")
}

func want(t testing.TB, d classes.Decision, v classes.Verdict, layer classes.Layer, reasonHas ...string) {
	t.Helper()
	if d.Verdict != v || (layer != "" && d.Layer != layer) {
		t.Fatalf("decision = %s by %s (%q); want %s by %s", d.Verdict, d.Layer, d.Reason, v, layer)
	}
	for _, s := range reasonHas {
		// On Windows the paths in a reason are in lower case.
		if !strings.Contains(strings.ToLower(d.Reason), strings.ToLower(s)) {
			t.Fatalf("reason %q does not cite %q", d.Reason, s)
		}
	}
}

func decide(env classes.Env, r *classes.Rules, ev providers.Event) classes.Decision {
	return classes.Decide(context.Background(), env, r, ev)
}

// ledger is an in-memory ExceptionLedger.
type ledger struct {
	mu   sync.Mutex
	used map[int64]bool
}

func newLedger() *ledger { return &ledger{used: map[int64]bool{}} }

func (l *ledger) Use(_ context.Context, _ string, id int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used[id] {
		return false, nil
	}
	l.used[id] = true
	return true, nil
}

// fakeGit is a GitState with a fixed branch and commit set; the scripts
// whose path ends in one of changed differ from the base of the branch
// (classes.TableScripts), the others came from the repository.
type fakeGit struct {
	branch  string
	staged  []string
	all     []string
	changed []string
}

func (g fakeGit) Changed(_ context.Context, _ string, p string) (bool, error) {
	k := strings.ToLower(filepath.ToSlash(p))
	for _, c := range g.changed {
		if strings.HasSuffix(k, "/"+strings.ToLower(c)) {
			return true, nil
		}
	}
	return false, nil
}

func (g fakeGit) Branch(context.Context, string) (string, error) { return g.branch, nil }

func (g fakeGit) Committed(_ context.Context, _ string, all bool) ([]string, error) {
	if all {
		return append(append([]string{}, g.staged...), g.all...), nil
	}
	return g.staged, nil
}
