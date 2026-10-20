package providers_test

import (
	"encoding/json"
	"errors"
	"runtime"
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
// CA-NATIVO-11
//
// The --settings file is in the worktree, which the agent writes: it is
// generated from scratch, so nothing planted there survives (disableAllHooks,
// hooks, env, a wider permissions block), and disableAllHooks is false,
// which wins over the local, project and user files.
func TestClaudeSettingsAreGeneratedFromScratchAndUseThePOSIXFormOfWindowsPaths(t *testing.T) {
	t.Parallel()
	nc, err := nativeOf(t, providers.ClaudeCode).Native(policy())
	if err != nil {
		t.Fatal(err)
	}
	planted := `{"statusLine": {"type": "command", "command": "mestre hook"}, "autoMemoryEnabled": true, "permissions": {"allow": ["Bash(x)"]},
		"disableAllHooks": true, "hooks": {"PreToolUse": []}, "env": {"MESTRE_AGENT_ID": ""}, "apiKeyHelper": "cat /x"}`
	out := merged(t, nc.Files[0], planted)
	var s map[string]any
	if err := json.Unmarshal([]byte(out), &s); err != nil || s["statusLine"] == nil {
		t.Fatalf("settings %s: %v", out, err)
	}
	for _, want := range []string{`"Edit(//c/Mestre/wt/src/**)"`, `"Read(//etc/**)"`, `"disableBypassPermissionsMode": "disable"`, `"autoMemoryEnabled": false`, `"disableAllHooks": false`} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings lack %s:\n%s", want, out)
		}
	}
	// The env is the Mestre's pin of the variables of the hooks, never the
	// planted one (ClaudeHookEnv).
	if env, _ := s["env"].(map[string]any); len(env) != 1 || env["MESTRE_AGENT_ID"] != "a1" {
		t.Fatalf("env %v: want only the pinned MESTRE_AGENT_ID", s["env"])
	}
	for _, gone := range []string{"Bash(x)", `"hooks"`, "apiKeyHelper"} {
		if strings.Contains(out, gone) {
			t.Fatalf("the planted %s survived:\n%s", gone, out)
		}
	}
	if again := merged(t, nc.Files[0], "[1]"); again != merged(t, nc.Files[0], "") {
		t.Fatalf("a file that is not an object does not give the same file:\n%s", again)
	}
	if slices.Contains(nc.Args, "WebSearch") {
		t.Fatalf("args %v: any domain keeps the search", nc.Args)
	}
}

// CA-NATIVO-11 (the settings.json of the profile and the agent file below)
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
	before := `{"trustedWorkspaces": ["/w"], "theme": "dark", "permissions": {"deny": ["command(rm*)", "command(git push*)"], "ask": ["command(make*)"], "allow": ["command(*)"]},
		"toolPermission": "always-proceed", "allowNonWorkspaceAccess": true, "enableTerminalSandbox": false, "agentMode": "accept-edits", "mcpServers": {"x": {}}}`
	out := merged(t, settings, before)
	if strings.Contains(out, "git push") || !strings.Contains(out, "command(rm*)") || !strings.Contains(out, "trustedWorkspaces") || !strings.Contains(out, `"request-review"`) {
		t.Fatalf("settings with the deny fallback:\n%s", out)
	}
	// C10: only the whitelist survives; permissions keeps deny and ask.
	if !strings.Contains(out, `"theme": "dark"`) || !strings.Contains(out, "command(make*)") {
		t.Fatalf("a kept key was dropped:\n%s", out)
	}
	for _, gone := range []string{"command(*)", "allowNonWorkspaceAccess", "enableTerminalSandbox", "agentMode", "mcpServers", "always-proceed"} {
		if strings.Contains(out, gone) {
			t.Fatalf("the planted %s survived:\n%s", gone, out)
		}
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
	cur := "---\nname: mestre-a1\ndescription: x\nmainAgent: true\npermissionMode: always-proceed\nmcpServers:\n  evil: {}\nmodel: other\ntools:\n  - write_to_file\n  - view_file\n---\nCorpo da camada do Mestre.\n"
	out := merged(t, nc.Files[0], cur)
	if !strings.HasSuffix(out, "---\nCorpo da camada do Mestre.\n") || !strings.Contains(out, "name: mestre-a1\n") ||
		strings.Count(out, "tools:") != 1 || strings.Contains(out, "write_to_file") || !strings.Contains(out, `"view_file"`) {
		t.Fatalf("agent file:\n%s", out)
	}
	// The frontmatter is generated from scratch: nothing planted survives.
	for _, gone := range []string{"permissionMode", "mcpServers", "evil", "model: other", "description: x"} {
		if strings.Contains(out, gone) {
			t.Fatalf("the planted %q survived:\n%s", gone, out)
		}
	}
	if again := merged(t, nc.Files[0], out); again != out {
		t.Fatalf("a second merge changed the file:\n%s", again)
	}
	if _, err := nativeOf(t, providers.Antigravity).Native(providers.NativePolicy{AgentID: "a1", Worktree: "/wt"}); err == nil {
		t.Fatal("antigravity without the profile HOME")
	}
}

// CA-NATIVO-09
//
// C7: a class without write never runs in -s read-only, which has no
// network and would cut the `mestre` CLI from the local channel: it runs in
// workspace-write with no extra writable root, and the gap is flagged.
func TestCodexFlagsFollowWriteAndNetwork(t *testing.T) {
	t.Parallel()
	p := policy()
	p.NoWrite = true
	nc, _ := nativeOf(t, providers.Codex).Native(p)
	want := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true", "-c", "sandbox_workspace_write.writable_roots=[]", "--search"}
	if p.OS == "windows" || runtime.GOOS == "windows" {
		want = slices.Insert(want, 4, "-c", `windows.sandbox="unelevated"`)
	}
	if !slices.Equal(nc.Args, want) {
		t.Fatalf("args = %q", nc.Args)
	}
	if !providers.CodexNoNativeWrite(p) {
		t.Fatal("a Codex class without write is not flagged without the native write barrier")
	}
	p.NoWrite = false
	if providers.CodexNoNativeWrite(p) {
		t.Fatal("a Codex class that writes is flagged")
	}
	p.NoWrite = true
	p.AnyDomain, p.Domains = false, []string{"github.com"}
	nc, _ = nativeOf(t, providers.Codex).Native(p)
	if slices.Contains(nc.Args, "--search") {
		t.Fatalf("args = %q: no hosted search with a list of domains", nc.Args)
	}
	if _, ok := providers.NativeOf(providers.NewNull()); ok {
		t.Fatal("the null adapter has native controls")
	}
}

// CA-NATIVO-14
//
// On Windows the model may run a command through Bash (Git Bash) or through
// PowerShell: the command rules name both tools; on Linux, only Bash.
func TestClaudeCommandRulesNameThePowerShellToolOnWindowsOnly(t *testing.T) {
	t.Parallel()
	perms := func(goos string) (allow, deny []string) {
		p := policy()
		p.OS = goos
		p.AllowCommands = []string{"make *"}
		p.DenyCommands = []string{"*"}
		nc, err := nativeOf(t, providers.ClaudeCode).Native(p)
		if err != nil {
			t.Fatal(err)
		}
		var obj struct {
			Permissions struct{ Allow, Deny []string } `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(merged(t, nc.Files[0], "")), &obj); err != nil {
			t.Fatal(err)
		}
		return obj.Permissions.Allow, obj.Permissions.Deny
	}
	allow, deny := perms("windows")
	for _, want := range []string{"PowerShell(make *)", "Bash(make *)"} {
		if !slices.Contains(allow, want) {
			t.Errorf("windows allow %v lacks %s", allow, want)
		}
	}
	for _, want := range []string{"PowerShell(git push *)", "PowerShell(git push)", "Bash(git push *)", "PowerShell", "Bash"} {
		if !slices.Contains(deny, want) {
			t.Errorf("windows deny %v lacks %s", deny, want)
		}
	}
	allow, deny = perms("linux")
	for _, l := range [][]string{allow, deny} {
		for _, r := range l {
			if strings.HasPrefix(r, "PowerShell") {
				t.Errorf("linux rule %s names PowerShell", r)
			}
		}
	}
	if !slices.Contains(deny, "Bash(git push *)") {
		t.Errorf("linux deny %v lacks Bash(git push *)", deny)
	}
}
