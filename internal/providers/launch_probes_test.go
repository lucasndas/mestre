package providers_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// The probes of the verification rounds of the M3 audit against the start
// checks of Codex and Antigravity, kept as regression tests: every form of
// clap and of the flag package that could open the sandbox, the approvals,
// the hooks or another folder is refused, and the model and display
// options stay allowed.
func TestTheStartChecksRefuseTheAuditProbes(t *testing.T) {
	t.Parallel()
	codex := providers.NewCodex(providers.Options{})
	sandbox := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
	start := func(base []string) ([]string, error) {
		l, err := codex.BuildLaunch(providers.LaunchRequest{Base: providers.Base{Command: "codex", Args: base}, AgentID: "a1", Profile: "/p", ProviderArgs: sandbox})
		return l.Args, err
	}
	for _, b := range [][]string{
		{"-sdanger-full-access"}, {"-s=danger-full-access"}, {"-s", "danger-full-access"}, {"--sandbox=danger-full-access"},
		{"-cK=V"}, {"-cfeatures.hooks=false"}, {"-c", "features={hooks=false}"}, {"-c", `features={"hooks"=false}`},
		{"-c", "features . hooks=false"}, {"-c", `"features".hooks=false`}, {"-c", `'features'.hooks=false`},
		{"-c", ` features.hooks = false`}, {"--config=features.hooks=false"}, {"--config", "features.hooks=false"},
		{"-c", "sandbox_mode=danger-full-access"}, {"-c", `sandbox_workspace_write.writable_roots=["/"]`},
		{"-c", `tui={"features"={hooks=false}}`}, {"-c", `model={features={hooks=false}}`},
		{"-c", "model_reasoning_effort=[{hooks=false}]"}, {"-c", "tui.hooks=1"},
		{"-c", "use_legacy_landlock=true"}, {"-c", "default_permissions=x"}, {"-c", "shell_environment_policy.inherit=none"},
		{"-c", `notify=["x"]`}, {"-c", `model_providers.x.base_url="http://e"`}, {"-c", `mcp_servers.x.command="sh"`},
		{"--dangerously-bypass-approvals-and-sandbox"}, {"--yolo"}, {"--full-auto"}, {"-a", "never"}, {"-anever"},
		{"--ask-for-approval=never"}, {"-a=never"}, {"-m", "-x"}, {"-p", "x"}, {"-px"}, {"--profile=x"},
		{"exec"}, {"e"}, {"resume", "x"}, {"--"}, {"-i", "x.png"}, {"-C", "/"}, {"-C/"}, {"--cd", "/"},
		{"--remote", "ws://x"}, {"-h"}, {"-V"}, {"--enable=x"}, {"--disable", "hooks"}, {"--search"},
		{"-c", "model=x", "--", "-c", "features.hooks=false"}, {"-s"}, {"-c"}, {"-s", "read-only"},
	} {
		if args, err := start(b); err == nil {
			t.Errorf("codex base %q accepted: %q", b, args)
		}
	}
	for _, g := range [][]string{
		{"--model", "gpt-5"}, {"-m", "o3"}, {"-mo3"}, {"--model=gpt-5"}, {"-c", "model_reasoning_effort=high"},
		{"-c", `model_reasoning_effort="high"`}, {"-c", "model=gpt-5"}, {"--oss"}, {"--local-provider", "ollama"},
		{"--no-alt-screen"}, {"-c", "tui.animations=false"}, {"-c", "hide_agent_reasoning=true"},
		{"-c", "model_verbosity=low"}, {"--no-daemon"}, {"-a", "on-request"},
	} {
		args, err := start(g)
		if err != nil {
			t.Errorf("codex base %q refused: %v", g, err)
			continue
		}
		if n := len(args); n < 2 || args[n-2] != "-c" || args[n-1] != providers.CodexHooksOn {
			t.Errorf("codex base %q: the hooks are not switched on last: %q", g, args)
		}
	}

	a := agy(t, providers.Options{})
	for _, b := range [][]string{
		{"-dangerously-skip-permissions"}, {"--remote-control"}, {"-remote-control"}, {"--remote-control=true"},
		{"--mode", "accept-edits"}, {"--mode=accept-edits"}, {"-mode", "accept-edits"}, {"-mode=accept-edits"},
		{"--MODE", "accept-edits"}, {"--sandbox=0"}, {"--sandbox=false"}, {"--sandbox", "false"}, {"-sandbox=f"},
		{"--sandbox=F"}, {"--agent", "x"}, {"-agent=x"}, {"--"}, {"--", "--dangerously-skip-permissions"},
		{"-"}, {"---mode", "x"}, {"--add-dir", "/"}, {"-p", "x"}, {"--print=x"}, {"-c"}, {"--continue"},
		{"--conversation", "x"}, {"--project=/"}, {"--model"}, {"--model", "m", "pos"},
	} {
		for _, engine := range []bool{false, true} {
			l, err := a.BuildLaunch(launchRequest(func(r *providers.LaunchRequest) {
				if engine {
					r.ProviderArgs = b
				} else {
					r.Base.Args = b
				}
			}))
			if err == nil {
				t.Errorf("antigravity %q (engine %v) accepted: %q", b, engine, l.Args)
			}
		}
	}
	for _, g := range [][]string{{"--model", "gemini-3"}, {"-model", "x"}, {"--model=x"}, {"--effort", "high"},
		{"--mode", "default"}, {"--sandbox"}, {"--sandbox=true"}, {"-sandbox"}, {"--disable-slash-commands"}} {
		if _, err := a.BuildLaunch(launchRequest(func(r *providers.LaunchRequest) { r.Base.Args = g })); err != nil {
			t.Errorf("antigravity base %q refused: %v", g, err)
		}
	}
}
