package fakeagent

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHookSemanticsPerProvider(t *testing.T) {
	t.Parallel()
	failed := errors.New("timeout")
	deny := []byte(`{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "no"}}`)
	cases := []struct {
		name  string
		p     Provider
		h     handler
		res   HookResult
		err   error
		block bool
	}{
		{"claude exit 2", Claude, handler{}, HookResult{ExitCode: 2, Stderr: []byte("why")}, nil, true},
		{"claude exit 1 lets it run", Claude, handler{}, HookResult{ExitCode: 1}, nil, false},
		{"claude timeout lets it run", Claude, handler{}, HookResult{}, failed, false},
		{"claude timeout with onFailure block", Claude, handler{block: true}, HookResult{}, failed, true},
		{"claude explicit deny", Claude, handler{}, HookResult{Stdout: deny}, nil, true},
		{"claude non-JSON output", Claude, handler{}, HookResult{Stdout: []byte("hi")}, nil, false},
		{"codex deny", Codex, handler{}, HookResult{Stdout: deny}, nil, true},
		{"codex failure lets it run", Codex, handler{}, HookResult{ExitCode: 3}, nil, false},
		{"codex timeout with onFailure block still lets it run", Codex, handler{block: true}, HookResult{}, failed, false},
		{"agy allow", Antigravity, handler{}, HookResult{Stdout: []byte(`{"decision":"allow"}`)}, nil, false},
		{"agy empty object", Antigravity, handler{}, HookResult{Stdout: []byte(`{}`)}, nil, true},
		{"agy exit 1", Antigravity, handler{}, HookResult{ExitCode: 1, Stdout: []byte(`{"decision":"allow"}`)}, nil, true},
		{"agy timeout", Antigravity, handler{}, HookResult{}, failed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := &agent{p: c.p}
			if got, _ := a.decide(c.h, c.res, c.err); got != c.block {
				t.Fatalf("block = %v, want %v", got, c.block)
			}
		})
	}
}

func TestParseScriptRejectsBadSteps(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"prompts:\n  - match: x\n    steps:\n      - {say: a, run: b}\n",
		"prompts:\n  - match: x\n    steps:\n      - wait: soon\n",
		"prompts:\n  - match: '('\n",
		"prompts:\n  - match: x\n    typo: y\n    steps:\n      - say: a\n",
		"prompts:\n  - match: x\n    steps:\n      - {say: a, sya: b}\n",
		"promtps: []\n",
	} {
		if _, err := ParseScript([]byte(src)); err == nil {
			t.Fatalf("accepted %q", src)
		}
	}
}

func TestOnFailureBlockIsHonouredOnlyForClaude(t *testing.T) {
	t.Parallel()
	const file = `{"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "gate", "onFailure": "block"}]}]}`
	var v any
	if err := json.Unmarshal([]byte(file), &v); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[Provider]bool{Claude: true, Codex: false} {
		hs, err := (&agent{p: p}).groupedHandlers(v, "hooks.json", "/w", "")
		if err != nil || len(hs) != 1 {
			t.Fatalf("%s: handlers = %v, err = %v", p, hs, err)
		}
		if hs[0].block != want {
			t.Fatalf("%s: block = %v, want %v", p, hs[0].block, want)
		}
	}
}
