package hook_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/providers"
)

const writesSrcYAML = `version: 1
files: {read: ["**"], write: ["src/**"]}
commands: {allow: ["npm test"], default: deny}
tools: {default: deny}
`

var ghKey = "ghp_" + strings.Repeat("a1B2", 9)

// file is a source file of twelve lines whose last one is the given one.
func file(last string) string {
	return strings.Repeat("const a = 1\n", 11) + last + "\n"
}

// writeCall is the native call of each provider that writes a file, in the
// shape the providers send it (the recordings of the fake agent).
func writeCall(p providers.ID, event, path, body string) []byte {
	var v any
	switch p {
	case providers.Antigravity:
		v = map[string]any{"conversationId": "c1", "executionNum": 1, "error": "", "toolCall": map[string]any{
			"name": "write_to_file", "args": map[string]any{"TargetFile": path, "CodeContent": body, "Overwrite": true, "Cwd": worktree()}}}
	case providers.Codex:
		patch := "*** Begin Patch\n*** Add File: " + path + "\n+" + strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n+") + "\n*** End Patch"
		v = map[string]any{"session_id": "s1", "turn_id": "t1", "cwd": worktree(), "hook_event_name": event,
			"tool_name": "apply_patch", "tool_input": map[string]any{"command": patch}, "tool_response": "Success"}
	default:
		v = map[string]any{"session_id": "s1", "cwd": worktree(), "hook_event_name": event, "tool_name": "Write",
			"tool_input": map[string]any{"file_path": path, "content": body}, "tool_response": map[string]any{"success": true}}
	}
	b, _ := json.Marshal(v)
	return b
}

// additionalContext is the text a hook gave the agent: the additionalContext
// of Claude Code and Codex, the injectSteps of Antigravity.
func additionalContext(t *testing.T, r run) string {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("stdout %q: %v", r.stdout, err)
	}
	if h, ok := m["hookSpecificOutput"].(map[string]any); ok {
		s, _ := h["additionalContext"].(string)
		return s
	}
	if steps, ok := m["injectSteps"].([]any); ok && len(steps) > 0 {
		s, _ := steps[0].(map[string]any)["ephemeralMessage"].(string)
		return s
	}
	return ""
}

// CA-PERM-26
func TestAKeyWrittenIntoAFileIsDeniedInPreToolAndToldAfterPostToolInTheThreeProviders(t *testing.T) {
	t.Parallel()
	src := filepath.Join(worktree(), "src", "x.ts")
	for _, p := range allProviders {
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			e := newEngine(t, writesSrcYAML)
			dial := engine(t, e)
			ask := func(event string, stdin []byte) run {
				return call{provider: p, event: event, stdin: stdin, dial: dial, files: newFiles(t), machine: machine()}.run(t.Context())
			}

			// pre_tool: denied with the place and the kind, never the value
			v, why := verdict(t, p, ask("PreToolUse", writeCall(p, "PreToolUse", src, file(`token = "`+ghKey+`"`))))
			if v != "deny" || !strings.Contains(why, "src/x.ts:12 github_token") || strings.Contains(why, ghKey) || strings.Contains(why, "a1B2a1B2") {
				t.Fatalf("a write with a key: %s (%q); want a denial that says src/x.ts:12 github_token and not the value", v, why)
			}
			// the same file reading the key from the environment is let through
			if v, why := verdict(t, p, ask("PreToolUse", writeCall(p, "PreToolUse", src, file(`token = process.env.GH_TOKEN`)))); v != "allow" {
				t.Fatalf("a write that reads the environment: %s (%q); want allow", v, why)
			}
			// a file that is outside the rules keeps its own denial
			if v, _ := verdict(t, p, ask("PreToolUse", writeCall(p, "PreToolUse", filepath.Join(worktree(), "docs", "x.md"), "# x\n"))); v != "deny" {
				t.Errorf("a write outside src/**: %s; want deny by the rules", v)
			}

			// post_tool: a write that went through with the key is told to the agent
			post := ask("PostToolUse", writeCall(p, "PostToolUse", src, file(`token = "`+ghKey+`"`)))
			warn := additionalContext(t, post)
			if p == providers.Antigravity {
				// its PostToolUse answers {}: the warning comes with the next invocation
				if warn != "" || strings.TrimSpace(post.stdout) != "{}" {
					t.Fatalf("PostToolUse of Antigravity = %q; want {}", post.stdout)
				}
				warn = additionalContext(t, ask("PreInvocation", mustJSON(map[string]any{"conversationId": "c1", "invocationNum": 1, "workspacePaths": []string{worktree()}})))
			}
			if !strings.Contains(warn, "src/x.ts:12 github_token") || strings.Contains(warn, ghKey) {
				t.Fatalf("the warning after the write = %q; want it to say src/x.ts:12 github_token and not the value", warn)
			}
			// ... once: the next invocation has nothing to say
			if p == providers.Antigravity {
				if again := additionalContext(t, ask("PreInvocation", mustJSON(map[string]any{"conversationId": "c1", "invocationNum": 2, "workspacePaths": []string{worktree()}}))); strings.Contains(again, "github_token") {
					t.Errorf("the warning came twice: %q", again)
				}
			}
			// a clean write says nothing
			if w := additionalContext(t, ask("PostToolUse", writeCall(p, "PostToolUse", src, file(`token = process.env.GH_TOKEN`)))); w != "" {
				t.Errorf("a clean write got the warning %q", w)
			}
		})
	}
}

// verdict is decision, where the allow of Codex is no output at all.
func verdict(t testing.TB, p providers.ID, r run) (string, string) {
	t.Helper()
	if p == providers.Codex && r.code == 0 && strings.TrimSpace(r.stdout) == "" {
		return "allow", ""
	}
	return decision(t, p, r)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Part of CA-PERM-26: the shell writes a file with a key by a here-document, a
// redirection, tee or sed -i, and the masker of the engine also knows the
// secrets the vault opened.
func TestAKeyThatTheShellWritesIntoAFileIsDeniedAndTheMaskerOfTheEngineKnowsTheVault(t *testing.T) {
	t.Parallel()
	e := newEngine(t, `version: 1
files: {read: ["**"], write: ["**"]}
commands: {allow: ["cat *", "echo *", "sed *", "tee *", "curl *"], default: deny}
tools: {default: deny}
`)
	e.Masker = logging.NewMasker()
	e.Masker.Register("correct-horse-battery-staple")
	dial := engine(t, e)
	ask := func(cmd string) (string, string) {
		return decision(t, providers.ClaudeCode, call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell(cmd), dial: dial, files: newFiles(t), machine: machine()}.run(t.Context()))
	}
	for cmd, want := range map[string]string{
		"cat > config.ts <<'EOF'\nconst a = 1\nconst b = 2\ntoken = \"" + ghKey + "\"\nEOF": "config.ts:3 github_token",
		"echo 'token = \"" + ghKey + "\"' >> .env.local":                                    ".env.local:1 github_token",
		"mkdir -p cfg 2>/dev/null; echo 'k = \"" + ghKey + "\"' > cfg/k.ts":                 "cfg/k.ts:1 github_token",
		"git add -A && cat > .env <<EOF\nTOKEN=\"" + ghKey + "\"\nEOF":                      ".env:1 github_token",
		"curl -s x; echo '" + ghKey + "' > a.ts":                                            "a.ts:1 github_token",
		"echo '" + ghKey + "' | tee keys.txt":                                               "keys.txt:1 github_token",
		"sed -i 's/X/" + ghKey + "/' src/x.ts":                                              "src/x.ts:1 github_token",
		"cat > notes.md <<EOF\npassword: correct-horse-battery-staple\nEOF":                 "notes.md:1 known_secret",
	} {
		if v, why := ask(cmd); v != "deny" || !strings.Contains(why, want) || strings.Contains(why, ghKey) || strings.Contains(why, "correct-horse") {
			t.Errorf("%q: %s (%q); want a denial that says %s and not the value", cmd, v, why, want)
		}
	}
	for _, cmd := range []string{
		"cat > config.ts <<'EOF'\ntoken = process.env.GH_TOKEN\nEOF",
		"echo hello > out.txt",
		"curl -H 'Authorization: Bearer " + ghKey + "' https://api.example.com > out.json",
	} {
		if v, why := ask(cmd); v != "allow" {
			t.Errorf("%q: %s (%q); want allow", cmd, v, why)
		}
	}
}
