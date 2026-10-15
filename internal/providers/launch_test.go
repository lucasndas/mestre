package providers_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// CA-ADAPT-08
func TestTypeMessageBytes(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	tests := []struct {
		id   providers.ID
		want string
	}{
		{providers.ClaudeCode, "\x1b[200~linha 1\nlinha 2\x1b[201~\r"},
		{providers.Codex, "\x1b[200~linha 1\nlinha 2\x1b[201~\r"},
		{providers.Antigravity, "\x1b[200~linha 1\nlinha 2\x1b[201~\r"},
		{providers.Null, "linha 1\rlinha 2\r"},
	}
	for _, tt := range tests {
		a, _ := reg.For(tt.id)
		got, err := a.TypeMessage("linha 1\nlinha 2")
		if err != nil || string(got) != tt.want {
			t.Errorf("%q: TypeMessage = %q, %v; want %q", tt.id, got, err, tt.want)
		}
	}
}

// A message cannot close the paste early and type the rest as keys, and a
// text above 8 KiB is not typed.
func TestTypeMessageDropsControlsAndRefusesLongText(t *testing.T) {
	t.Parallel()
	a := providers.NewClaudeCode(providers.Options{})
	got, err := a.TypeMessage("a\x1b[201~\x03b\r\nc")
	if err != nil || string(got) != "\x1b[200~a[201~b\nc\x1b[201~\r" {
		t.Errorf("TypeMessage = %q, %v", got, err)
	}
	if _, err := a.TypeMessage(strings.Repeat("x", providers.MaxTyped)); err != nil {
		t.Errorf("8 KiB: %v", err)
	}
	if _, err := a.TypeMessage(strings.Repeat("x", providers.MaxTyped+1)); !errors.Is(err, providers.ErrTooLong) {
		t.Errorf("8 KiB + 1: err = %v, want ErrTooLong", err)
	}
}

// CA-ADAPT-10
func TestBuildLaunchResumesWithTheBaseFlags(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	baseArgs := []string{"--model", "the-model"}
	tests := []struct {
		id           providers.ID
		command      string
		session      string
		providerArgs []string
		want         []string
	}{
		{providers.ClaudeCode, "claude", "2f1c7c1e-55d1-4b8f-9a51-0c1b7f3f5e10", claudeLayerArgs("a1"),
			append([]string{"--model", "the-model", "--resume=2f1c7c1e-55d1-4b8f-9a51-0c1b7f3f5e10"}, append(claudeLayerArgs("a1"), "--permission-mode", "default")...)},
		{providers.Codex, "codex", "019a0000-0000-7000-8000-000000000001", []string{"-c", `developer_instructions="x"`, "-s", "read-only"},
			[]string{"resume", "019a0000-0000-7000-8000-000000000001", "--model", "the-model", "-c", `developer_instructions="x"`, "-s", "read-only", "--no-daemon", "-a", "on-request", "-c", "features.hooks=true"}},
		{providers.Antigravity, "agy", "b38a07d5-f7a3-4e27-b4d0-43e9110f60bb", nil,
			[]string{"--model", "the-model", "--conversation", "b38a07d5-f7a3-4e27-b4d0-43e9110f60bb", "--agent", "mestre-a1"}},
	}
	for _, tt := range tests {
		a, _ := reg.For(tt.id)
		req := providers.LaunchRequest{
			Base:    providers.Base{Name: string(tt.id), Command: tt.command, Args: baseArgs, Env: []string{"BASE=1"}, Dir: "/work/mesa"},
			OS:      "linux",
			AgentID: "a1", TableID: "3-login", Worktree: "/work/mesa",
			ResumeSessionID: tt.session, NewSessionID: "not-used-on-resume",
			ProviderArgs: tt.providerArgs, Env: []string{"CLAUDE_CONFIG_DIR=/profiles/p", "HOME=/profiles/agy"}, Profile: "/profiles/p",
		}
		l, err := a.BuildLaunch(req)
		if err != nil {
			t.Fatalf("%q: %v", tt.id, err)
		}
		if l.Path != tt.command || !slices.Equal(l.Args, tt.want) || l.Dir != "/work/mesa" || l.SessionID != tt.session || !l.Resumed {
			t.Errorf("%q: launch = %+v, want %s %q", tt.id, l, tt.command, tt.want)
		}
		for _, kv := range []string{"BASE=1", "CLAUDE_CONFIG_DIR=/profiles/p", "MESTRE_AGENT_ID=a1", "MESTRE_MESA=3-login"} {
			if !slices.Contains(l.Env, kv) {
				t.Errorf("%q: env %v lacks %s", tt.id, l.Env, kv)
			}
		}
		// The start command of the same character: no resume, the same base flags.
		req.ResumeSessionID = ""
		start, err := a.BuildLaunch(req)
		if err != nil {
			t.Fatalf("%q start: %v", tt.id, err)
		}
		if start.Resumed || !slices.Equal(start.Args[:len(baseArgs)], baseArgs) {
			t.Errorf("%q start: %+v", tt.id, start)
		}
	}
}

func TestBuildLaunchStartsAndRefusesTheMostRecentSession(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	l, err := claude.BuildLaunch(claudeRequest("u1"))
	if want := append([]string{"--session-id", "u1"}, append(claudeLayerArgs("a1"), "--permission-mode", "default")...); err != nil || !slices.Equal(l.Args, want) || l.SessionID != "u1" {
		t.Errorf("claude start = %+v, %v; want %v", l, err, want)
	}
	if _, err := claude.BuildLaunch(claudeRequest("")); err == nil {
		t.Error("claude start without the session id the engine generates")
	}
	for _, c := range []struct {
		a    providers.Adapter
		args []string
	}{
		{claude, []string{"--continue"}},
		{claude, []string{"-c"}},
		{providers.NewCodex(providers.Options{}), []string{"--last"}},
	} {
		req := providers.LaunchRequest{Base: providers.Base{Command: "x", Args: c.args}, NewSessionID: "u1", ResumeSessionID: "s1"}
		if c.a == claude {
			req = claudeRequest("u1")
			req.Base.Args, req.ResumeSessionID = c.args, "s1"
		}
		if _, err := c.a.BuildLaunch(req); err == nil {
			t.Errorf("%s %v was accepted", c.a.Info().ID, c.args)
		}
	}
	shell, err := providers.NewNull().BuildLaunch(providers.LaunchRequest{Base: providers.Base{Command: "bash", Args: []string{"-l"}}, ResumeSessionID: "s1"})
	if err != nil || shell.Path != "bash" || !slices.Equal(shell.Args, []string{"-l"}) || shell.Resumed {
		t.Errorf("null = %+v, %v", shell, err)
	}
}

// M1 of the M3 audit: the variables of a base are checked at every start,
// not only when the base is saved. The ones that skip or move the
// provider's hooks, permissions, configuration or sandbox, that run code
// before the hooks, and the Mestre's own are refused (also in lower case:
// Windows does not tell case apart); the harmless ones go through.
func TestABaseMayNotSetTheVariablesOfTheSafety(t *testing.T) {
	t.Parallel()
	requests := map[providers.ID]func() (providers.Adapter, providers.LaunchRequest){
		providers.ClaudeCode: func() (providers.Adapter, providers.LaunchRequest) {
			return providers.NewClaudeCode(providers.Options{}), claudeRequest("u1")
		},
		providers.Codex: func() (providers.Adapter, providers.LaunchRequest) {
			return providers.NewCodex(providers.Options{}), providers.LaunchRequest{Base: providers.Base{Command: "codex"}, Profile: "/profiles/c", AgentID: "a1", ProviderArgs: []string{"-s", "read-only"}}
		},
		providers.Antigravity: func() (providers.Adapter, providers.LaunchRequest) {
			return providers.NewAntigravity(providers.Options{}), launchRequest(nil)
		},
		providers.Null: func() (providers.Adapter, providers.LaunchRequest) {
			return providers.NewNull(), providers.LaunchRequest{Base: providers.Base{Command: "bash"}}
		},
	}
	every := []string{"MESTRE_AGENT_ID=x", "mestre_endpoint=unix:///tmp/x", "MESTRE_CLI=/tmp/x"}
	agents := []string{"LD_PRELOAD=/tmp/x.so", "NODE_OPTIONS=--require /tmp/x.js", "BASH_ENV=/tmp/x", "ENV=/tmp/x",
		"GIT_CONFIG_GLOBAL=/tmp/g", "GIT_CONFIG_PARAMETERS='alias.st=!sh'", "GIT_CONFIG_COUNT=1", "DYLD_INSERT_LIBRARIES=/x"}
	refused := map[providers.ID][]string{
		providers.ClaudeCode: slices.Concat(every, agents, []string{
			"CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_SAFE_MODE=1", "CLAUDE_CONFIG_DIR=/elsewhere", "claude_config_dir=/elsewhere",
			"CLAUDE_CODE_SHELL_PREFIX=/tmp/w", "CLAUDE_ENV_FILE=/tmp/e", "CLAUDE_CODE_PROCESS_WRAPPER=/tmp/w",
			"CLAUDE_CODE_GIT_BASH_PATH=/tmp/bash", "CLAUDE_CODE_SHELL=/tmp/bash", "SHELL=/tmp/bash",
			"CLAUDE_CODE_DISABLE_POWERSHELL_CMD_RM_DENY=1", "CLAUDE_CODE_DISABLE_DANGEROUS_RM_TIMEOUT=1",
			"CLAUDE_CODE_DISABLE_PERMISSION_PROMPT_NOTIFY_HOOKS=1", "CLAUDE_CODE_DISABLE_ANYTHING_NEW=1",
			"CLAUDE_CODE_PLUGIN_DIRS=/tmp/p", "CLAUDE_AFK_TIMEOUT_MS=1",
		}),
		providers.Codex:       slices.Concat(every, agents, []string{"CODEX_HOME=/elsewhere", "CODEX_SANDBOX=seatbelt", "CODEX_UNSAFE_ALLOW_NO_SANDBOX=1"}),
		providers.Antigravity: slices.Concat(every, agents, []string{"HOME=/home/x", `USERPROFILE=C:\x`, "home=/x"}),
		providers.Null:        every,
	}
	allowed := map[providers.ID][]string{
		providers.ClaudeCode: {"FOO=1", "ANTHROPIC_MODEL=opus", "CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "HTTPS_PROXY=http://p:3128", "LANG=pt_BR.UTF-8"},
		providers.Codex:       {"FOO=1", "HTTPS_PROXY=http://p:3128", "RUST_LOG=info"},
		providers.Antigravity: {"FOO=1", "LANG=pt_BR.UTF-8"},
		providers.Null:        {"FOO=1", "LD_PRELOAD=/usr/lib/x.so", "HOME=/home/x"},
	}
	for id, mk := range requests {
		for _, kv := range refused[id] {
			a, req := mk()
			req.Base.Env = []string{"OK=1", kv}
			if l, err := a.BuildLaunch(req); err == nil {
				t.Errorf("%q: base env %s was accepted: %q", id, kv, l.Env)
			} else if key, _, _ := strings.Cut(kv, "="); !strings.Contains(err.Error(), key) {
				t.Errorf("%q: %s: error %q does not name the variable", id, kv, err)
			}
		}
		for _, kv := range allowed[id] {
			a, req := mk()
			req.Base.Env = []string{kv}
			if l, err := a.BuildLaunch(req); err != nil || !slices.Contains(l.Env, kv) {
				t.Errorf("%q: base env %s: %v (env %q); want it kept", id, kv, err, l.Env)
			}
		}
	}
}
