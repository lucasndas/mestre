package hook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const agent = "alice"

// root is an absolute folder of this system for paths nobody touches.
func root() string {
	if runtime.GOOS == "windows" {
		return `C:\m`
	}
	return "/m"
}

func worktree() string { return filepath.Join(root(), "workspaces", "w", "mesas", "1-login") }

// machine is the folders of the dispatcher in the tests.
func machine() *hook.Machine {
	return &hook.Machine{
		Instance: "principal", Agents: filepath.Join(root(), "agentes"), Queue: filepath.Join(root(), "fila"),
		Layout: classes.Layout{Data: []string{filepath.Join(root(), "dados")}, Agents: filepath.Join(root(), "agentes")},
	}
}

// memFiles is the disk of the dispatcher in memory. With strict set, any
// use fails the test (CA-HOOK-01).
type memFiles struct {
	t      testing.TB
	strict bool
	mu     sync.Mutex
	files  map[string][]byte
	reads  int
}

func newFiles(t testing.TB) *memFiles { return &memFiles{t: t, files: map[string][]byte{}} }

func (m *memFiles) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.strict {
		m.t.Errorf("the dispatcher read %s", path)
	}
	b, ok := m.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}

func (m *memFiles) CreateNew(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.strict {
		m.t.Errorf("the dispatcher created %s", path)
	}
	if _, ok := m.files[path]; ok {
		return fs.ErrExist
	}
	m.files[path] = append([]byte(nil), data...)
	return nil
}

func (m *memFiles) put(path string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		m.t.Fatal(err)
	}
	m.mu.Lock()
	m.files[path] = b
	m.mu.Unlock()
}

// queued returns the records the dispatcher left in the queue folder.
func (m *memFiles) queued(dir string) []hook.QueueRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for p := range m.files {
		if filepath.Dir(p) == dir {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	var out []hook.QueueRecord
	for _, p := range names {
		var r hook.QueueRecord
		if err := json.Unmarshal(m.files[p], &r); err != nil {
			m.t.Fatalf("queue file %s: %v", p, err)
		}
		out = append(out, r)
	}
	return out
}

// snapshot writes the policy.json and env.json of the character.
func (m *memFiles) snapshot(mc *hook.Machine, r *classes.Rules) {
	dir := filepath.Join(mc.Agents, agent)
	if r != nil {
		m.put(filepath.Join(dir, classes.SnapshotName), r)
	}
	m.put(filepath.Join(dir, hook.EnvName), hook.CharacterEnv{Version: hook.EnvVersion, AgentID: agent, Worktree: worktree()})
}

// rules builds effective rules from a permissions.yaml.
func rules(t testing.TB, yaml string) *classes.Rules {
	t.Helper()
	p, err := classes.ParsePermissions([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := classes.Build(classes.Layers{AgentID: agent, Class: "qa", Permissions: p, Now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const qaYAML = `version: 1
files: {read: ["**"], write: ["**"], deny: ["src/**"]}
commands: {allow: ["npm test"], ask: ["npm install *"], deny: ["curl *"], default: deny}
tools: {default: deny}
`

// payload is the stdin of a pre_tool of a shell command, per provider.
func payload(p providers.ID, command string) []byte {
	var v any
	switch p {
	case providers.Antigravity:
		v = map[string]any{"conversationId": "c1", "executionNum": 1, "toolCall": map[string]any{
			"name": "run_command", "args": map[string]any{"CommandLine": command, "Cwd": worktree()}}}
	default:
		v = map[string]any{"session_id": "s1", "prompt_id": "p1", "turn_id": "t1", "cwd": worktree(),
			"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": command}}
	}
	b, _ := json.Marshal(v)
	return b
}

var allProviders = []providers.ID{providers.ClaudeCode, providers.Codex, providers.Antigravity}

// run is one call of the dispatcher.
type run struct {
	code           int
	stdout, stderr string
}

type call struct {
	provider providers.ID
	event    string
	stdin    []byte
	env      map[string]string
	dial     func(string) localchan.Dialer
	files    hook.Files
	machine  *hook.Machine
	runner   hook.Runner
	// decide is the degraded decision (nil: the real one); reader, when
	// set, is the stdin instead of stdin.
	decide func(context.Context, classes.Env, *classes.Rules, providers.Event) classes.Decision
	reader io.Reader
}

func (c call) run(ctx context.Context) run {
	var out, errb bytes.Buffer
	env := c.env
	if env == nil {
		env = map[string]string{localchan.EnvAgentID: agent, localchan.EnvEndpoint: "unix:///run/mestre/principal/cli.sock"}
	}
	var stdin io.Reader = bytes.NewReader(c.stdin)
	if c.reader != nil {
		stdin = c.reader
	}
	code := hook.Run(ctx, hook.Env{
		Getenv: func(k string) string { return env[k] }, Stdin: stdin, Stdout: &out, Stderr: &errb,
		Clock: clock.Real{}, Dial: c.dial, Files: c.files, Machine: c.machine, ClassHooks: c.runner,
		Resolve: func(p string) (string, error) { return p, nil }, DecideDegraded: c.decide,
	}, []string{"--provider", string(c.provider), "--event", c.event})
	return run{code, out.String(), errb.String()}
}

// decision reads the decision of a pre_tool answer, whatever the provider:
// "allow" or "deny" and the reason.
func decision(t testing.TB, p providers.ID, r run) (string, string) {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("%s: exit %d, stderr %q", p, r.code, r.stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("%s: stdout %q: %v", p, r.stdout, err)
	}
	if p == providers.Antigravity {
		reason, _ := m["reason"].(string)
		return m["decision"].(string), reason
	}
	h := m["hookSpecificOutput"].(map[string]any)
	reason, _ := h["permissionDecisionReason"].(string)
	return h["permissionDecision"].(string), reason
}

// engine is a rig whose hook handler is h, with alice's terminal and the
// process that calls from it.
func engine(t *testing.T, h localchan.Handler) func(string) localchan.Dialer {
	t.Helper()
	r := localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{Local: map[string]localchan.Handler{localchan.CmdHook: h}}})
	r.Start(t)
	r.Character("t1", agent)
	r.Process("alice-proc", "t1")
	return func(string) localchan.Dialer { return r.Dialer("alice-proc") }
}

// silent is an engine that takes the call and never answers.
type silent struct {
	mu    sync.Mutex
	calls []providers.Event
}

func (s *silent) Handle(ctx context.Context, c *localchan.Call) localchan.Response {
	var ev providers.Event
	_ = json.Unmarshal(c.Request.Args[0], &ev)
	s.mu.Lock()
	s.calls = append(s.calls, ev)
	s.mu.Unlock()
	<-ctx.Done()
	return localchan.Fail(localchan.CodeInternal, "stopped", "")
}

func (s *silent) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// down is a dialer of an engine that is not there.
func down(string) localchan.Dialer {
	return func(context.Context) (net.Conn, error) { return nil, errors.New("connection refused") }
}

func has(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }
