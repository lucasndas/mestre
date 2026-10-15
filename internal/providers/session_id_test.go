package providers_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// A session id that comes from a hook payload travels as an argument of the
// resume command: one that looks like an option is refused by every provider.
func TestResumeSessionIDThatIsAnOptionIsRefused(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	for _, tt := range []struct {
		id      providers.ID
		cmd     string
		args    []string
		session string
	}{
		{providers.Codex, "codex", []string{"-c", `developer_instructions="x"`, "-s", "read-only"}, "--dangerously-bypass-approvals-and-sandbox"},
		{providers.ClaudeCode, "claude", claudeLayerArgs("a1"), "--dangerously-skip-permissions"},
		{providers.Antigravity, "agy", nil, "--yolo"},
		{providers.ClaudeCode, "claude", claudeLayerArgs("a1"), "a b"},
		{providers.Codex, "codex", nil, "-x"},
	} {
		a, _ := reg.For(tt.id)
		_, err := a.BuildLaunch(providers.LaunchRequest{
			Base: providers.Base{Name: string(tt.id), Command: tt.cmd, Dir: "/work/mesa"}, OS: "linux", AgentID: "a1", TableID: "3-login",
			Worktree: "/work/mesa", ResumeSessionID: tt.session, ProviderArgs: tt.args,
			Env: []string{"CLAUDE_CONFIG_DIR=/p", "HOME=/p", "CODEX_HOME=/p"}, Profile: "/p"})
		if err == nil {
			t.Errorf("%s resumed with %q", tt.id, tt.session)
		}
	}
}

func TestValidSessionID(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"2f1c7c1e-55d1-4b8f-9a51-0c1b7f3f5e10", "019a0000-0000-7000-8000-000000000001", "s", "conv_1:2.3"} {
		if !providers.ValidSessionID(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-x", "--resume", "a b", "a\nb", "a=b", "../x", "a/b"} {
		if providers.ValidSessionID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
