package providers_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

func nativeOf(t testing.TB, id providers.ID) providers.NativeTranslator {
	t.Helper()
	a, err := providers.NewRegistry(providers.Options{}).For(id)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := providers.NativeOf(a)
	if !ok {
		t.Fatalf("%s: no native translation", id)
	}
	return tr
}

func policy() providers.NativePolicy {
	return providers.NativePolicy{
		AgentID: "a1", Worktree: `C:\Mestre\wt`, ProfileHome: `C:\Mestre\contas\agy`, OS: "windows",
		Write: []string{"src/**"}, DenyFiles: []string{"/etc/**"}, FixedDenyCommands: []string{"git push *"},
		AnyDomain: true,
	}
}

func merged(t testing.TB, f providers.NativeFile, cur string) string {
	t.Helper()
	var in []byte
	if cur != "" {
		in = []byte(cur)
	}
	out, err := f.Merge(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// CA-NATIVO-08
func TestClaudeSettingsKeepOtherKeysAndUseThePOSIXFormOfWindowsPaths(t *testing.T) {
	t.Parallel()
	nc, err := nativeOf(t, providers.ClaudeCode).Native(policy())
	if err != nil {
		t.Fatal(err)
	}
	out := merged(t, nc.Files[0], `{"statusLine": {"type": "command", "command": "mestre hook"}, "autoMemoryEnabled": true, "permissions": {"allow": ["Bash(x)"]}}`)
	var s map[string]any
	if err := json.Unmarshal([]byte(out), &s); err != nil || s["statusLine"] == nil {
		t.Fatalf("settings %s: %v", out, err)
	}
	for _, want := range []string{`"Edit(//c/Mestre/wt/src/**)"`, `"Read(//etc/**)"`, `"disableBypassPermissionsMode": "disable"`, `"autoMemoryEnabled": false`} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings lack %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Bash(x)") {
		t.Fatalf("the old permissions survived:\n%s", out)
	}
	if _, err := nc.Files[0].Merge([]byte("[1]")); !errors.Is(err, providers.ErrNativeFile) {
		t.Fatalf("a settings file that is not an object: %v", err)
	}
	if slices.Contains(nc.Args, "WebSearch") {
		t.Fatalf("args %v: any domain keeps the search", nc.Args)
	}
}

func TestTheFallbacksTakeTheirRulesToTheDispatcher(t *testing.T) {
	t.Parallel()
	p := policy()
	p.Fallbacks = providers.NativeFallbacks{ClaudePaths: providers.ClaudePathsOff, AntigravityNoDeny: true}
	nc, err := nativeOf(t, providers.ClaudeCode).Native(p)
	if err != nil {
		t.Fatal(err)
	}
	if out := merged(t, nc.Files[0], ""); strings.Contains(out, "//c/Mestre/wt") || !strings.Contains(out, "Bash(git push *)") {
		t.Fatalf("with the path rules off:\n%s", out)
	}
	p.Fallbacks.ClaudePaths = providers.ClaudePathsRelative
	nc, _ = nativeOf(t, providers.ClaudeCode).Native(p)
	if out := merged(t, nc.Files[0], ""); !strings.Contains(out, `"Edit(src/**)"`) {
		t.Fatalf("relative path rules:\n%s", out)
	}

	ag, err := nativeOf(t, providers.Antigravity).Native(p)
	if err != nil {
		t.Fatal(err)
	}
	settings := ag.Files[1]
	before := `{"trustedWorkspaces": ["/w"], "permissions": {"deny": ["command(rm*)", "command(git push*)"]}, "toolPermission": "always-proceed"}`
	out := merged(t, settings, before)
	if strings.Contains(out, "git push") || !strings.Contains(out, "command(rm*)") || !strings.Contains(out, "trustedWorkspaces") || !strings.Contains(out, `"request-review"`) {
		t.Fatalf("settings with the deny fallback:\n%s", out)
	}
	p.Fallbacks.AntigravityNoDeny = false
	ag, _ = nativeOf(t, providers.Antigravity).Native(p)
	if out := merged(t, ag.Files[1], before); strings.Count(out, "command(git push*)") != 1 {
		t.Fatalf("the fixed rule once:\n%s", out)
	}
	if _, err := ag.Files[1].Merge([]byte(`{"permissions": {"deny": "x"}}`)); !errors.Is(err, providers.ErrNativeFile) {
		t.Fatalf("an invalid permissions.deny: %v", err)
	}
}

func TestTheAntigravityAgentFileKeepsItsBodyAndFrontmatter(t *testing.T) {
	t.Parallel()
	p := policy()
	p.NoWrite = true
	nc, err := nativeOf(t, providers.Antigravity).Native(p)
	if err != nil {
		t.Fatal(err)
	}
	cur := "---\nname: mestre-a1\ndescription: x\nmainAgent: true\ntools:\n  - write_to_file\n  - view_file\n---\nCorpo da camada do Mestre.\n"
	out := merged(t, nc.Files[0], cur)
	if !strings.HasSuffix(out, "---\nCorpo da camada do Mestre.\n") || !strings.Contains(out, "name: mestre-a1\n") ||
		strings.Count(out, "tools:") != 1 || strings.Contains(out, "write_to_file") || !strings.Contains(out, `"view_file"`) {
		t.Fatalf("agent file:\n%s", out)
	}
	if again := merged(t, nc.Files[0], out); again != out {
		t.Fatalf("a second merge changed the file:\n%s", again)
	}
	if _, err := nativeOf(t, providers.Antigravity).Native(providers.NativePolicy{AgentID: "a1", Worktree: "/wt"}); err == nil {
		t.Fatal("antigravity without the profile HOME")
	}
}

func TestCodexFlagsFollowWriteAndNetwork(t *testing.T) {
	t.Parallel()
	p := policy()
	p.NoWrite = true
	nc, _ := nativeOf(t, providers.Codex).Native(p)
	if !slices.Equal(nc.Args, []string{"-s", "read-only", "-c", "sandbox_workspace_write.network_access=true", "--search"}) {
		t.Fatalf("args = %q", nc.Args)
	}
	p.AnyDomain, p.Domains = false, []string{"github.com"}
	nc, _ = nativeOf(t, providers.Codex).Native(p)
	if slices.Contains(nc.Args, "--search") {
		t.Fatalf("args = %q: no hosted search with a list of domains", nc.Args)
	}
	if _, ok := providers.NativeOf(providers.NewNull()); ok {
		t.Fatal("the null adapter has native controls")
	}
}
