package providers_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// claudeLayerArgs are the arguments the Mestre layer and the native rules
// of a character add to the command (DeliverMestreLayer and Native).
func claudeLayerArgs(id string) []string {
	return []string{"--append-system-prompt-file", ".mestre/agents/" + id + "/MESTRE.md", "--settings", ".mestre/agents/" + id + "/claude-settings.json"}
}

// claudeRequest is a valid request for the start of the character a1.
func claudeRequest(session string) providers.LaunchRequest {
	return providers.LaunchRequest{
		Base: providers.Base{Name: "claude", Command: "claude", Dir: "/work/mesa"},
		OS:   "linux", AgentID: "a1", TableID: "3-login",
		NewSessionID: session, ProviderArgs: claudeLayerArgs("a1"),
		Env: []string{"CLAUDE_CONFIG_DIR=/profiles/p"},
	}
}

func object(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, b)
	}
	return m
}

func list(v any) []any { l, _ := v.([]any); return l }

func mapOf(v any) map[string]any { m, _ := v.(map[string]any); return m }

func apply(t *testing.T, f providers.ManagedFile, cur []byte) []byte {
	t.Helper()
	out, err := f.Merge(cur)
	if err != nil {
		t.Fatalf("merge %s: %v", f.Path, err)
	}
	return out
}

// The events of the hook file, with their matcher, timeout and onFailure.
var wantClaudeHooks = []struct {
	event   string
	matcher bool
	timeout float64
	block   bool
}{
	{"SessionStart", false, 60, false},
	{"UserPromptSubmit", false, 30, false},
	{"PreToolUse", true, 30, true},
	{"PermissionRequest", true, 30, true},
	{"PostToolUse", true, 60, false},
	{"PostToolUseFailure", true, 60, false},
	{"Stop", false, 60, false},
	{"StopFailure", false, 60, false},
	{"Notification", false, 60, false},
	{"PreCompact", false, 60, false},
	{"SessionEnd", false, 60, false},
}

const personsSettings = `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo mine"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "echo stop"}]}],
    "Elicitation": [{"hooks": [{"type": "command", "command": "echo other event"}]}]
  },
  "statusLine": {"type": "command", "command": "echo my status"},
  "bigNumber": 12345678901234567890
}`

// CA-CLAUDE-01
func TestHookFilesKeepTheHooksOfThePersonAndAddTheManagedPartOnce(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			wt := "/work/mesa"
			if goos == "windows" {
				wt = `C:\Mestre\work\mesa`
			}
			files, err := providers.NewClaudeCode(providers.Options{}).HookFiles(providers.HookFilesRequest{OS: goos, Root: wt})
			if err != nil || len(files) != 1 {
				t.Fatalf("HookFiles = %v, %v", files, err)
			}
			if !strings.HasSuffix(strings.ReplaceAll(files[0].Path, `\`, "/"), "/.claude/settings.local.json") {
				t.Errorf("file = %s, want .claude/settings.local.json of the worktree", files[0].Path)
			}
			first := apply(t, files[0], []byte(personsSettings))
			second := apply(t, files[0], first) // HookFiles ran twice
			if string(first) != string(second) {
				t.Fatalf("the second run changed the file:\n%s\n---\n%s", first, second)
			}

			got := object(t, second)
			// The person's content, intact.
			if got["model"] != "opus" || mapOf(got["statusLine"])["command"] != "echo my status" ||
				!slices.Equal(list(mapOf(got["permissions"])["allow"]), []any{"Bash(ls)"}) {
				t.Errorf("the person's keys changed: %v", got)
			}
			if !strings.Contains(string(second), "12345678901234567890") {
				t.Errorf("a big number of the person lost its text:\n%s", second)
			}
			hooks := mapOf(got["hooks"])
			if other := list(hooks["Elicitation"]); len(other) != 1 {
				t.Errorf("a hook of the person on another event changed: %v", hooks["Elicitation"])
			}
			pre := list(hooks["PreToolUse"])
			if len(pre) != 2 || mapOf(pre[0])["matcher"] != "Bash" || mapOf(list(mapOf(pre[0])["hooks"])[0])["command"] != "echo mine" {
				t.Errorf("PreToolUse: the person's group must stay first and intact: %v", pre)
			}
			if stop := list(hooks["Stop"]); len(stop) != 2 || mapOf(list(mapOf(stop[0])["hooks"])[0])["command"] != "echo stop" {
				t.Errorf("Stop: %v", stop)
			}

			// The managed part: one handler per event, once.
			managed := 0
			for _, w := range wantClaudeHooks {
				var ours []map[string]any
				for _, g := range list(hooks[w.event]) {
					for _, h := range list(mapOf(g)["hooks"]) {
						if c, _ := mapOf(h)["command"].(string); strings.Contains(c, "hook --provider claude-code --event "+w.event) {
							ours = append(ours, mapOf(g))
							if mapOf(h)["timeout"] != w.timeout {
								t.Errorf("%s: timeout = %v, want %v", w.event, mapOf(h)["timeout"], w.timeout)
							}
							if _, has := mapOf(h)["onFailure"]; has != w.block || (has && mapOf(h)["onFailure"] != "block") {
								t.Errorf("%s: onFailure = %v, want block only in PreToolUse and PermissionRequest", w.event, mapOf(h)["onFailure"])
							}
						}
					}
				}
				if len(ours) != 1 {
					t.Errorf("%s: %d managed handlers, want 1", w.event, len(ours))
					continue
				}
				managed++
				if m, has := ours[0]["matcher"]; has != w.matcher || (has && m != "*") {
					t.Errorf("%s: matcher = %v, want * on the tool events only", w.event, m)
				}
				if groups := list(hooks[w.event]); !isManagedGroup(groups[len(groups)-1], w.event) {
					t.Errorf("%s: the managed handler must come after the person's", w.event)
				}
			}
			if managed != len(wantClaudeHooks) {
				t.Errorf("managed events = %d", managed)
			}
			if got["statusLine"] == nil || strings.Contains(string(second), "StatusLine") {
				t.Errorf("the statusLine goes in the --settings file, not in the hook file:\n%s", second)
			}
		})
	}
}

func isManagedGroup(g any, event string) bool {
	for _, h := range list(mapOf(g)["hooks"]) {
		if c, _ := mapOf(h)["command"].(string); strings.Contains(c, "hook --provider claude-code --event "+event) {
			return true
		}
	}
	return false
}

// The hook file is created when it does not exist, and a file that is not a
// JSON object is never touched; the handlers of an older version are replaced.
func TestHookFilesCreateRefuseAndReplace(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	files, err := claude.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	created := object(t, apply(t, files[0], nil))
	if len(mapOf(created["hooks"])) != len(wantClaudeHooks) {
		t.Errorf("created file hooks = %v", created["hooks"])
	}

	for _, bad := range []string{`{"hooks": `, `[]`, `"x"`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`, `{} {}`} {
		if _, err := files[0].Merge([]byte(bad)); !errors.Is(err, providers.ErrNativeFile) {
			t.Errorf("%q: err = %v, want ErrNativeFile (the file is not touched)", bad, err)
		}
	}

	old := `{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "mestre hook --provider claude-code --event PreToolUse"}]}],
		"Retired": [{"hooks": [{"type": "command", "command": "mestre hook --provider claude-code --event Retired"}, {"type": "command", "command": "echo mine"}]}]}}`
	out := object(t, apply(t, files[0], []byte(old)))
	hooks := mapOf(out["hooks"])
	if pre := list(hooks["PreToolUse"]); len(pre) != 1 || strings.Contains(mapOf(list(mapOf(pre[0])["hooks"])[0])["command"].(string), "exec") == false {
		t.Errorf("the plain handler of an older version stays next to the new one: %v", pre)
	}
	if r := list(hooks["Retired"]); len(r) != 1 || len(list(mapOf(r[0])["hooks"])) != 1 {
		t.Errorf("Retired: the handler of the Mestre goes, the person's stays: %v", r)
	}

	if _, err := claude.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "relative"}); err == nil {
		t.Error("HookFiles of a relative worktree")
	}
	if _, err := claude.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "/w", Binary: `/opt/me stre/"; rm`}); err == nil {
		t.Error("HookFiles with a binary path that breaks the shell line")
	}
	if _, err := claude.HookFiles(providers.HookFilesRequest{OS: "plan9", Root: "/w"}); err == nil {
		t.Error("HookFiles for a system that is neither linux nor windows")
	}
}

// The line of the hook calls `mestre hook` only when MESTRE_AGENT_ID and the
// binary exist (the folder may be cloned without the Mestre), by the shell
// of each system.
func TestHookLineIsGuardedAndNamesTheStableBinary(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	command := func(goos, binary string) string {
		root := "/w"
		if goos == "windows" {
			root = `C:\w`
		}
		files, err := claude.HookFiles(providers.HookFilesRequest{OS: goos, Root: root, Binary: binary})
		if err != nil {
			t.Fatal(err)
		}
		h := mapOf(list(mapOf(list(mapOf(object(t, apply(t, files[0], nil))["hooks"])["PreToolUse"])[0])["hooks"])[0])
		return h["command"].(string)
	}
	if got, want := command("linux", ""), `[ -n "$MESTRE_AGENT_ID" ] && command -v "${MESTRE_CLI:-mestre}" >/dev/null 2>&1 && exec "${MESTRE_CLI:-mestre}" hook --provider claude-code --event PreToolUse; exit 0`; got != want {
		t.Errorf("linux = %s", got)
	}
	if got := command("linux", "/opt/mestre/bin/mestre"); !strings.Contains(got, `"${MESTRE_CLI:-/opt/mestre/bin/mestre}"`) {
		t.Errorf("linux with the stable binary = %s", got)
	}
	if got, want := command("windows", ""), `if defined MESTRE_AGENT_ID if exist "%MESTRE_CLI%" "%MESTRE_CLI%" hook --provider claude-code --event PreToolUse`; got != want {
		t.Errorf("windows = %s", got)
	}
	if got := command("windows", `C:\Program Files\Mestre\mestre.exe`); !strings.Contains(got, `if exist "C:\Program Files\Mestre\mestre.exe" "C:\Program Files\Mestre\mestre.exe" hook`) {
		t.Errorf("windows with the stable binary = %s", got)
	}
}

// The "Se falhar" of the statusLine: it goes to the hook file, the --settings
// file loses it, and a statusLine of the person is never replaced.
func TestStatusLineFallbackMovesItToTheHookFile(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	native := func(fb providers.NativeFallbacks) providers.NativeFile {
		nc, err := claude.(providers.NativeTranslator).Native(providers.NativePolicy{AgentID: "a1", OS: "linux", Worktree: "/w", Fallbacks: fb})
		if err != nil {
			t.Fatal(err)
		}
		return nc.Files[0]
	}
	statusLine := func(b []byte) string { c, _ := mapOf(object(t, b)["statusLine"])["command"].(string); return c }

	def := apply(t, providers.ManagedFile(native(providers.NativeFallbacks{})), nil)
	if c := statusLine(def); !strings.Contains(c, "hook --provider claude-code --event StatusLine") {
		t.Errorf("default: the --settings file has statusLine %q", c)
	}
	fb := providers.NativeFallbacks{ClaudeStatusLineInLocal: true}
	if c := statusLine(apply(t, providers.ManagedFile(native(fb)), def)); c != "" {
		t.Errorf("fallback: the --settings file kept the statusLine %q", c)
	}
	files, err := claude.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "/w", Fallbacks: fb})
	if err != nil {
		t.Fatal(err)
	}
	if c := statusLine(apply(t, files[0], nil)); !strings.Contains(c, "--event StatusLine") {
		t.Errorf("fallback: the hook file has statusLine %q", c)
	}
	if c := statusLine(apply(t, files[0], []byte(personsSettings))); c != "echo my status" {
		t.Errorf("fallback replaced the statusLine of the person: %q", c)
	}
	// The --settings file is the Mestre's whole, generated from scratch: a
	// statusLine found there (planted in the worktree) is not kept.
	if c := statusLine(apply(t, providers.ManagedFile(native(providers.NativeFallbacks{})), []byte(personsSettings))); !strings.Contains(c, "--event StatusLine") {
		t.Errorf("default kept a statusLine planted in the --settings file: %q", c)
	}
}

// DeliverMestreLayer: the append file, and the folder of the character for
// its skills.
func TestDeliverMestreLayer(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	l, err := claude.DeliverMestreLayer(providers.LayerRequest{Path: ".mestre/agents/a1/MESTRE.md", CharacterSkills: true})
	if want := []string{"--append-system-prompt-file", ".mestre/agents/a1/MESTRE.md", "--add-dir", ".mestre/agents/a1"}; err != nil || !slices.Equal(l.Args, want) || len(l.Files) != 0 {
		t.Errorf("with skills = %+v, %v; want %v", l, err, want)
	}
	l, err = claude.DeliverMestreLayer(providers.LayerRequest{Path: `C:\wt\.mestre\agents\a1\MESTRE.md`})
	if want := []string{"--append-system-prompt-file", `C:\wt\.mestre\agents\a1\MESTRE.md`}; err != nil || !slices.Equal(l.Args, want) {
		t.Errorf("without skills = %+v, %v; want %v", l, err, want)
	}
	for _, bad := range []string{"", "MESTRE.md", "/home/x/AGENTS.md", "/w/.mestre/MESTRE.md", "/w/other/a1/MESTRE.md"} {
		if _, err := claude.DeliverMestreLayer(providers.LayerRequest{Path: bad}); err == nil {
			t.Errorf("%q was accepted as a Mestre layer", bad)
		}
	}
}

// launchFlags lists the flags of an argument list.
func launchFlags(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			name, _, _ := strings.Cut(a, "=")
			out = append(out, name)
		}
	}
	return out
}

var forbiddenClaudeFlags = []string{
	"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--bare", "--system-prompt", "--system-prompt-file",
	"--system-prompt-snapshot", "--setting-sources", "-p", "--print", "--continue", "-c",
}

// CA-CLAUDE-02
func TestBuildLaunchHasTheLayerTheSettingsAndTheProfileAndNoForbiddenFlag(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	bases := []providers.Base{
		{Name: "claude", Command: "claude", Dir: "/work/mesa"},
		{Name: "claude-opus", Command: "/usr/local/bin/claude", Args: []string{"--model", "opus", "--verbose"}, Env: []string{"FOO=1"}, Dir: "/work/mesa"},
		{Name: "claude-win", Command: `C:\Users\mestre-agente\claude.exe`, Args: []string{"--model=sonnet"}, Dir: `C:\Mestre\wt`},
		{Name: "claude-allowed", Command: "claude", Args: []string{"--allowedTools", "Read", "Grep", "--permission-mode", "default"}},
	}
	for _, goos := range []string{"linux", "windows"} {
		for _, base := range bases {
			for _, skills := range []bool{false, true} {
				for _, resume := range []string{"", "2f1c7c1e-55d1-4b8f-9a51-0c1b7f3f5e10"} {
					worktree, profile := "/work/mesa", "/profiles/p"
					if goos == "windows" {
						worktree, profile = `C:\Mestre\wt`, `C:\Mestre\contas\p`
					}
					layer, err := claude.DeliverMestreLayer(providers.LayerRequest{Path: worktree + "/.mestre/agents/a1/MESTRE.md", CharacterSkills: skills})
					if err != nil {
						t.Fatal(err)
					}
					nc, err := claude.(providers.NativeTranslator).Native(providers.NativePolicy{AgentID: "a1", OS: goos, Worktree: worktree, NoWrite: true, Offline: true})
					if err != nil {
						t.Fatal(err)
					}
					l, err := claude.BuildLaunch(providers.LaunchRequest{
						Base: base, OS: goos, AgentID: "a1", TableID: "3-login", NewSessionID: "u1", ResumeSessionID: resume,
						ProviderArgs: slices.Concat(layer.Args, nc.Args), Env: []string{"CLAUDE_CONFIG_DIR=" + profile},
					})
					if err != nil {
						t.Fatalf("%s/%s/skills=%v/resume=%q: %v", goos, base.Name, skills, resume, err)
					}
					flags := launchFlags(l.Args)
					for _, want := range []string{"--append-system-prompt-file", "--settings"} {
						if !slices.Contains(flags, want) {
							t.Errorf("%s/%s: %v lacks %s", goos, base.Name, l.Args, want)
						}
					}
					if !slices.Contains(l.Env, "CLAUDE_CONFIG_DIR="+profile) {
						t.Errorf("%s/%s: env %v lacks CLAUDE_CONFIG_DIR", goos, base.Name, l.Env)
					}
					for _, bad := range forbiddenClaudeFlags {
						if slices.Contains(flags, bad) {
							t.Errorf("%s/%s: %v has the forbidden %s", goos, base.Name, l.Args, bad)
						}
					}
					if i := slices.Index(l.Args, "--permission-mode"); i < 0 || l.Args[i+1] != "default" {
						t.Errorf("%s/%s: %v lacks --permission-mode default", goos, base.Name, l.Args)
					}
					if n := strings.Count(strings.Join(l.Args, " "), "--permission-mode"); n != 1 {
						t.Errorf("%s/%s: --permission-mode %d times", goos, base.Name, n)
					}
					if dirs := slices.Index(flags, "--add-dir"); (dirs >= 0) != skills {
						t.Errorf("%s/%s: --add-dir present = %v, want %v (the skills of the character)", goos, base.Name, dirs >= 0, skills)
					}
					if resume != "" && (!l.Resumed || !slices.Contains(l.Args, "--resume")) || resume == "" && slices.Contains(l.Args, "--resume") {
						t.Errorf("%s/%s: resume %q gave %v", goos, base.Name, resume, l.Args)
					}
				}
			}
		}
	}
}

func TestBuildLaunchRefusesWhatTheDocumentForbids(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	tests := []struct {
		name   string
		change func(*providers.LaunchRequest)
	}{
		{"no layer", func(r *providers.LaunchRequest) { r.ProviderArgs = r.ProviderArgs[2:] }},
		{"no settings", func(r *providers.LaunchRequest) { r.ProviderArgs = r.ProviderArgs[:2] }},
		{"layer of another character", func(r *providers.LaunchRequest) { r.ProviderArgs[1] = ".mestre/agents/a2/MESTRE.md" }},
		{"layer outside .mestre", func(r *providers.LaunchRequest) { r.ProviderArgs[1] = "/etc/MESTRE.md" }},
		{"settings of another character", func(r *providers.LaunchRequest) { r.ProviderArgs[3] = ".mestre/agents/a2/claude-settings.json" }},
		{"settings from the base", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--settings", "/tmp/x.json"} }},
		{"layer from the base", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--append-system-prompt-file", "/tmp/x.md"} }},
		{"no profile", func(r *providers.LaunchRequest) { r.Env = nil }},
		{"empty profile", func(r *providers.LaunchRequest) { r.Env = []string{"CLAUDE_CONFIG_DIR="} }},
		{"no agent", func(r *providers.LaunchRequest) { r.AgentID = "" }},
		{"permission mode bypass", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--permission-mode", "bypassPermissions"} }},
		{"permission mode auto =", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--permission-mode=auto"} }},
		{"permission mode dontAsk", func(r *providers.LaunchRequest) {
			r.ProviderArgs = append(r.ProviderArgs, "--permission-mode", "dontAsk")
		}},
		{"permission mode acceptEdits", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--permission-mode", "acceptEdits"} }},
		{"permission mode plan", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--permission-mode", "plan"} }},
		{"add-dir of another folder", func(r *providers.LaunchRequest) { r.ProviderArgs = append(r.ProviderArgs, "--add-dir", "/etc") }},
		{"add-dir of another character", func(r *providers.LaunchRequest) {
			r.ProviderArgs = append(r.ProviderArgs, "--add-dir", ".mestre/agents/a2")
		}},
		{"add-dir with two folders", func(r *providers.LaunchRequest) {
			r.ProviderArgs = append(r.ProviderArgs, "--add-dir", ".mestre/agents/a1", "/etc")
		}},
		{"add-dir from the base", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--add-dir", ".mestre/agents/a1"} }},
		{"session id from the base", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--session-id", "x"} }},
		{"resume from the base", func(r *providers.LaunchRequest) { r.Base.Args = []string{"--resume", "x"} }},
	}
	for _, f := range forbiddenClaudeFlags {
		tests = append(tests,
			struct {
				name   string
				change func(*providers.LaunchRequest)
			}{"base " + f, func(r *providers.LaunchRequest) { r.Base.Args = []string{f} }},
			struct {
				name   string
				change func(*providers.LaunchRequest)
			}{"engine " + f, func(r *providers.LaunchRequest) { r.ProviderArgs = append(r.ProviderArgs, f) }},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := claudeRequest("u1")
			req.ProviderArgs = slices.Clone(req.ProviderArgs)
			tt.change(&req)
			if l, err := claude.BuildLaunch(req); err == nil {
				t.Errorf("accepted: %v", l.Args)
			}
		})
	}
	// What is allowed stays allowed: the folder of the character itself.
	req := claudeRequest("u1")
	req.ProviderArgs = append(req.ProviderArgs, "--add-dir", ".mestre/agents/a1")
	req.ProviderArgs[1] = ".mestre/agents/a1/MESTRE.md"
	if _, err := claude.BuildLaunch(req); err != nil {
		t.Errorf("add-dir of the own folder: %v", err)
	}
}

// The "Se falhar" of the multi-line paste: the text goes through the file of
// the long messages.
func TestMultilineViaFileFallback(t *testing.T) {
	t.Parallel()
	def := providers.NewClaudeCode(providers.Options{})
	fb := providers.NewClaudeCode(providers.Options{ClaudeMultilineViaFile: true})
	if _, err := def.TypeMessage("a\nb"); err != nil {
		t.Errorf("default: %v", err)
	}
	if _, err := fb.TypeMessage("a\nb"); !errors.Is(err, providers.ErrTooLong) {
		t.Errorf("fallback with two lines: err = %v, want ErrTooLong", err)
	}
	if b, err := fb.TypeMessage("one line"); err != nil || string(b) != "\x1b[200~one line\x1b[201~\r" {
		t.Errorf("fallback with one line = %q, %v", b, err)
	}
}

// The shell tool of Claude Code on Windows is PowerShell, handled as shell;
// a name nobody mapped is `other`.
func TestPowerShellIsShell(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{Resolve: noLinks})
	for name, want := range map[string]providers.ToolKind{"PowerShell": providers.KindShell, "Bash": providers.KindShell, "Monitor": providers.KindOther} {
		in := `{"session_id": "s", "cwd": "/w", "prompt_id": "p1", "tool_name": "` + name + `", "tool_input": {"command": "Get-ChildItem"}, "tool_use_id": "t"}`
		ev, err := claude.NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: []byte(in), ReceivedAt: at})
		if err != nil || ev.Tool.Kind != want {
			t.Errorf("%s: %+v, %v; want kind %s", name, ev.Tool, err, want)
		}
	}
}
